// Copyright 2026 Matrix Origin
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

type stabilityConnection struct {
	endpoint string
	pool     *sql.DB
	conn     *sql.Conn
}

func runStabilityScenario(ctx context.Context, s loadedScenario, queries []query, o options, config *mysql.Config, report ScenarioReport) ScenarioReport {
	report.EffectiveConcurrency = 1 // Repeat the same input sequentially so each run has a clear outcome.
	for _, q := range queries {
		if len(q.ExactIDs) > 0 {
			report.ExactTruthQueries++
		}
	}
	if o.repeat < s.MinRepeats {
		return stabilityFailure(report, fmt.Sprintf("requires at least %d repetitions, got %d", s.MinRepeats, o.repeat))
	}
	endpoints := o.queryEndpoints
	if len(endpoints) == 0 {
		endpoints = []string{config.Addr}
	}
	connections := make([]stabilityConnection, 0, len(endpoints))
	defer func() {
		for _, entry := range connections {
			_ = entry.conn.Close()
			_ = entry.pool.Close()
		}
	}()
	report.PhysicalPlans = make(map[string]string)
	report.EndpointVersions = make(map[string]string)
	firstArgs, err := argsFor(queries[0], s.Args)
	if err != nil {
		return stabilityFailure(report, err.Error())
	}
	for _, endpoint := range endpoints {
		endpointConfig := *config
		endpointConfig.Addr = endpoint
		pool, err := sql.Open("mysql", endpointConfig.FormatDSN())
		if err != nil {
			return stabilityFailure(report, fmt.Sprintf("endpoint %s: %v", endpoint, err))
		}
		pool.SetMaxOpenConns(1)
		connCtx, cancel := context.WithTimeout(ctx, o.timeout)
		conn, err := pool.Conn(connCtx)
		cancel()
		if err != nil {
			_ = pool.Close()
			return stabilityFailure(report, fmt.Sprintf("endpoint %s: %v", endpoint, err))
		}
		connections = append(connections, stabilityConnection{endpoint, pool, conn})
		versionCtx, versionCancel := context.WithTimeout(ctx, o.timeout)
		var serverVersion string
		err = conn.QueryRowContext(versionCtx, "SELECT VERSION()").Scan(&serverVersion)
		versionCancel()
		if err != nil {
			return stabilityFailure(report, fmt.Sprintf("endpoint %s version: %v", endpoint, err))
		}
		report.EndpointVersions[endpoint] = serverVersion
		for _, statement := range s.SessionSQL {
			statementCtx, statementCancel := context.WithTimeout(ctx, o.timeout)
			_, err = conn.ExecContext(statementCtx, statement)
			statementCancel()
			if err != nil {
				return stabilityFailure(report, fmt.Sprintf("endpoint %s session setup: %v", endpoint, err))
			}
		}
		plan, err := physicalPlan(ctx, conn, s.SQL, firstArgs, o.timeout)
		if err != nil {
			report.PlanError = fmt.Sprintf("endpoint %s: %v", endpoint, err)
			return stabilityFailure(report, report.PlanError)
		}
		report.PhysicalPlans[endpoint] = plan
		if report.Plan == "" {
			report.Plan = plan
		}
	}
	for i := 0; i < o.warmup; i++ {
		q := queries[i%len(queries)]
		args, err := argsFor(q, s.Args)
		if err != nil {
			return stabilityFailure(report, err.Error())
		}
		entry := connections[i%len(connections)]
		if _, err := selectIDs(ctx, entry.conn, s.SQL, args, s.TopK, o.timeout, s.IDColumn, true); err != nil {
			return stabilityFailure(report, fmt.Sprintf("warmup query %s on %s: %v", q.ID, entry.endpoint, err))
		}
	}
	report.Results = make([]QueryResult, len(queries)*o.repeat)
	start := time.Now()
	for iteration := 0; iteration < o.repeat; iteration++ {
		for queryIndex, q := range queries {
			entry := connections[(iteration+queryIndex)%len(connections)]
			result := QueryResult{ID: q.ID, Iteration: iteration, Endpoint: entry.endpoint}
			args, err := argsFor(q, s.Args)
			if err == nil {
				queryStart := time.Now()
				result.IDs, err = selectIDs(ctx, entry.conn, s.SQL, args, s.TopK, o.timeout, s.IDColumn, true)
				result.LatencyMS = float64(time.Since(queryStart).Microseconds()) / 1000
			}
			if err != nil {
				result.Error = err.Error()
			} else {
				result.SQLSucceeded = true
			}
			report.Results[iteration*len(queries)+queryIndex] = result
		}
	}
	elapsed := time.Since(start).Seconds()
	evaluateStability(&report, queries, o.repeat, s.ExpectedRows)
	aggregateScenarioResults(&report, elapsed)
	return report
}

func checkPlanEvidence(plan string, required map[string]int) error {
	for term, count := range required {
		if found := strings.Count(plan, term); found < count {
			return fmt.Errorf("physical plan has %d instances of %q; need %d", found, term, count)
		}
	}
	return nil
}

func stabilityFailure(report ScenarioReport, message string) ScenarioReport {
	report.Failures = 1
	report.Error = message
	return report
}

func physicalPlan(ctx context.Context, conn *sql.Conn, statement string, args []any, timeout time.Duration) (string, error) {
	return readQueryPlan(ctx, conn, "EXPLAIN PHYPLAN ", statement, args, timeout)
}

func readQueryPlan(ctx context.Context, conn sqlQueryer, prefix, statement string, args []any, timeout time.Duration) (string, error) {
	planCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rows, err := conn.QueryContext(planCtx, prefix+statement, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return "", err
		}
		for _, value := range values {
			part := planValue(value)
			if builder.Len()+len(part)+1 > 1<<20 {
				return "", fmt.Errorf("physical plan exceeds 1 MiB")
			}
			builder.WriteString(part)
			builder.WriteByte('\n')
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func evaluateStability(report *ScenarioReport, queries []query, repeat, expectedRows int) {
	report.WorstOverlap = 1
	for queryIndex, q := range queries {
		outcome := StabilityResult{QueryID: q.ID, WorstOverlap: 1, Executions: repeat}
		var baseline []string
		baselineSet := false
		firstOrder := ""
		orders := make(map[string]bool)
		seen := make(map[string]bool)
		exactSignature := ""
		if len(q.ExactIDs) > 0 {
			exactSignature = multisetSignature(q.ExactIDs)
		}
		for iteration := 0; iteration < repeat; iteration++ {
			result := &report.Results[iteration*len(queries)+queryIndex]
			note := func(message string) {
				if report.QualityMode == "observe" {
					result.Observations = append(result.Observations, message)
				} else if result.Error == "" {
					result.Error = message
				}
			}
			if result.SQLSucceeded {
				outcome.SQLSuccesses++
			}
			if result.Error != "" {
				outcome.Failures++
				continue
			}
			if len(result.IDs) == 0 && !report.AllowEmpty {
				note("empty result")
				if report.QualityMode != "observe" {
					outcome.Failures++
					continue
				}
			}
			if expectedRows > 0 && len(result.IDs) != expectedRows {
				note(fmt.Sprintf("got %d rows, want %d", len(result.IDs), expectedRows))
			}
			if err := checkAllowedIDs(q, result.IDs); err != nil {
				note(err.Error())
			}
			signature := multisetSignature(result.IDs)
			seen[signature] = true
			ordered, _ := json.Marshal(result.IDs)
			order := string(ordered)
			orders[order] = true
			if !baselineSet {
				baseline = result.IDs
				firstOrder = order
				baselineSet = true
			}
			overlap, changed := multisetOverlap(baseline, result.IDs)
			result.Score = overlap
			if overlap < outcome.WorstOverlap {
				outcome.WorstOverlap = overlap
			}
			if changed > outcome.MaxChangedIDs {
				outcome.MaxChangedIDs = changed
			}
			if overlap < 1 && result.Error == "" {
				note(fmt.Sprintf("ID multiset changed: %d of %d positions differ from first execution", changed, max(len(baseline), len(result.IDs))))
			}
			if order != firstOrder && overlap == 1 {
				outcome.ReorderedExecutions++
				if report.CheckOrder && result.Error == "" {
					note("ID ordering changed from first execution")
				}
			}
			if exactSignature != "" && signature != exactSignature && result.Error == "" {
				note("ID multiset differs from frozen exact_ids")
			}
			result.Pass = result.Error == ""
			if !result.Pass {
				outcome.Failures++
			}
		}
		outcome.DistinctResults = len(seen)
		if report.CheckOrder {
			outcome.DistinctOrders = len(orders)
		}
		if outcome.DistinctResults == 0 {
			outcome.WorstOverlap = 0
		}
		if outcome.DistinctResults > report.MaxDistinctResults {
			report.MaxDistinctResults = outcome.DistinctResults
		}
		if outcome.WorstOverlap < report.WorstOverlap {
			report.WorstOverlap = outcome.WorstOverlap
		}
		if outcome.MaxChangedIDs > report.MaxChangedIDs {
			report.MaxChangedIDs = outcome.MaxChangedIDs
		}
		report.Stability = append(report.Stability, outcome)
	}
}

func multisetSignature(ids []string) string {
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	encoded, _ := json.Marshal(ordered)
	return string(encoded)
}

func multisetOverlap(a, b []string) (float64, int) {
	if len(a) == 0 && len(b) == 0 {
		return 1, 0
	}
	counts := make(map[string]int, len(a))
	for _, id := range a {
		counts[id]++
	}
	var common int
	for _, id := range b {
		if counts[id] > 0 {
			common++
			counts[id]--
		}
	}
	denominator := max(len(a), len(b))
	return float64(common) / float64(denominator), denominator - common
}
