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
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

type Profile struct {
	QueryLimit          int      `json:"query_limit"`
	Repeat              int      `json:"repeat"`
	Concurrency         int      `json:"concurrency"`
	Warmup              int      `json:"warmup"`
	Timeout             string   `json:"timeout"`
	QueryEndpoints      []string `json:"query_endpoints"`
	SQLAddress          string   `json:"sql_address,omitempty"`
	ConcurrencyLevels   []int    `json:"concurrency_levels,omitempty"`
	StabilityRepeat     int      `json:"stability_repeat,omitempty"`
	StabilityQueryLimit int      `json:"stability_query_limit,omitempty"`
	MixedScenarios      []string `json:"mixed_scenarios,omitempty"`
}

type Stage struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	Rows    int64   `json:"rows,omitempty"`
	Error   string  `json:"error,omitempty"`
}

type InputFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Rows   int64  `json:"rows"`
}

type QueryResult struct {
	ID           string          `json:"id"`
	Iteration    int             `json:"iteration"`
	LatencyMS    float64         `json:"latency_ms"`
	IDs          []string        `json:"ids,omitempty"`
	Score        float64         `json:"score"`
	Pass         bool            `json:"pass"`
	SQLSucceeded bool            `json:"sql_succeeded"`
	Error        string          `json:"error,omitempty"`
	Endpoint     string          `json:"endpoint,omitempty"`
	Quality      *QualityMetrics `json:"quality,omitempty"`
}

type StabilityResult struct {
	QueryID             string  `json:"query_id"`
	Executions          int     `json:"executions"`
	SQLSuccesses        int     `json:"sql_successes"`
	Failures            int     `json:"failures"`
	DistinctResults     int     `json:"distinct_results"`
	WorstOverlap        float64 `json:"worst_overlap"`
	MaxChangedIDs       int     `json:"max_changed_ids"`
	DistinctOrders      int     `json:"distinct_orders,omitempty"`
	ReorderedExecutions int     `json:"reordered_executions,omitempty"`
}

type ScenarioReport struct {
	ExecutionMode        string            `json:"execution_mode,omitempty"`
	BaseScenarioID       string            `json:"base_scenario_id,omitempty"`
	ID                   string            `json:"id"`
	Route                string            `json:"route"`
	Oracle               string            `json:"oracle"`
	MinScore             float64           `json:"min_score"`
	TopK                 int               `json:"top_k"`
	CandidateK           int               `json:"candidate_k"`
	SQL                  string            `json:"sql,omitempty"`
	VectorSQL            string            `json:"vector_sql,omitempty"`
	FulltextSQL          string            `json:"fulltext_sql,omitempty"`
	SessionSQL           []string          `json:"session_sql,omitempty"`
	PlanMustContain      map[string]int    `json:"plan_must_contain,omitempty"`
	ScenarioSHA256       string            `json:"scenario_sha256"`
	QueriesSHA256        string            `json:"queries_sha256"`
	SelectedQueries      int               `json:"selected_queries"`
	Executions           int               `json:"executions"`
	Failures             int               `json:"failures"`
	Repetitions          int               `json:"repetitions,omitempty"`
	SQLSuccesses         int               `json:"sql_successes"`
	SQLFailures          int               `json:"sql_failures"`
	AssertionFailures    int               `json:"assertion_failures"`
	MeasuredSeconds      float64           `json:"measured_seconds"`
	P50MS                float64           `json:"p50_ms"`
	P90MS                float64           `json:"p90_ms"`
	P95MS                float64           `json:"p95_ms"`
	P99MS                float64           `json:"p99_ms"`
	QPS                  float64           `json:"qps"`
	MeanScore            float64           `json:"mean_score"`
	Plan                 string            `json:"plan,omitempty"`
	FulltextPlan         string            `json:"fulltext_plan,omitempty"`
	PlanError            string            `json:"plan_error,omitempty"`
	Error                string            `json:"error,omitempty"`
	Results              []QueryResult     `json:"results"`
	Stability            []StabilityResult `json:"stability,omitempty"`
	MaxDistinctResults   int               `json:"max_distinct_results"`
	WorstOverlap         float64           `json:"worst_overlap"`
	MaxChangedIDs        int               `json:"max_changed_ids"`
	ExactTruthQueries    int               `json:"exact_truth_queries"`
	PhysicalPlans        map[string]string `json:"physical_plans,omitempty"`
	EndpointVersions     map[string]string `json:"endpoint_versions,omitempty"`
	EffectiveConcurrency int               `json:"effective_concurrency,omitempty"`
	QualityMode          string            `json:"quality_mode,omitempty"`
	NDCGGain             string            `json:"ndcg_gain,omitempty"`
	RelevantGrade        int               `json:"relevant_grade,omitempty"`
	CheckOrder           bool              `json:"check_order,omitempty"`
	AllowEmpty           bool              `json:"allow_empty,omitempty"`
}

type Report struct {
	RunKind          string               `json:"run_kind,omitempty"`
	ToolVersion      string               `json:"tool_version"`
	BinarySHA256     string               `json:"binary_sha256"`
	Dataset          string               `json:"dataset"`
	ManifestSHA256   string               `json:"manifest_sha256"`
	Inputs           []InputFile          `json:"inputs"`
	Database         string               `json:"database"`
	MatrixOneVersion string               `json:"matrixone_version,omitempty"`
	StartedAt        time.Time            `json:"started_at"`
	FinishedAt       time.Time            `json:"finished_at"`
	Status           string               `json:"status"`
	Profile          Profile              `json:"profile"`
	Stages           []Stage              `json:"stages"`
	Scenarios        []ScenarioReport     `json:"scenarios"`
	Errors           []string             `json:"errors,omitempty"`
	Cleanup          string               `json:"cleanup"`
	ResourceMetrics  string               `json:"resource_metrics"`
	IndexSQL         []string             `json:"index_sql,omitempty"`
	Environment      *EnvironmentEvidence `json:"environment,omitempty"`
}

func runBenchmark(ctx context.Context, p *pack, o options) (report Report, runErr error) {
	report = Report{
		ToolVersion: version, Dataset: p.Manifest.Dataset, ManifestSHA256: p.Digest,
		StartedAt: time.Now().UTC(), Status: "failed", ResourceMetrics: "unavailable",
		Profile:  Profile{QueryLimit: o.queryLimit, Repeat: o.repeat, Concurrency: o.concurrency, Warmup: o.warmup, Timeout: o.timeout.String(), ConcurrencyLevels: append([]int(nil), o.concurrencyLevels...), StabilityRepeat: o.stabilityRepeat, StabilityQueryLimit: o.stabilityQueryLimit, MixedScenarios: append([]string(nil), o.mixedScenarios...)},
		Cleanup:  "not_started",
		IndexSQL: append([]string(nil), p.Manifest.Indexes...),
	}
	for _, load := range p.Manifest.Loads {
		report.Inputs = append(report.Inputs, InputFile{load.File.Path, load.File.SHA256, load.Rows})
	}
	var inputs environmentInputs
	var queryStart, queryEnd time.Time
	defer func() {
		if inputs.Monitor != nil && !queryStart.IsZero() {
			report.Environment.Monitoring = collectMonitoring(ctx, *inputs.Monitor, queryStart, queryEnd)
			report.ResourceMetrics = report.Environment.Monitoring.Status
		} else if inputs.Monitor != nil {
			report.Environment.Monitoring.Reason = "query phase was not reached"
		}
		report.FinishedAt = time.Now().UTC()
		if runErr != nil {
			report.Errors = append(report.Errors, runErr.Error())
		} else if len(report.Errors) == 0 {
			report.Status = "passed"
		}
	}()
	var inputErr error
	inputs, inputErr = loadEnvironmentInputs(o)
	if inputErr != nil {
		return report, inputErr
	}
	report.Environment = newEnvironmentEvidence(inputs)
	runs, err := planScenarioRuns(p.Scenarios, o)
	if err != nil {
		return report, err
	}
	report.Scenarios = make([]ScenarioReport, len(runs))
	for i, run := range runs {
		report.Scenarios[i] = initialScenarioReport(run.Scenario, run.Options)
		report.Scenarios[i].Error = "not executed: database preparation did not complete"
	}
	if err := validateMixedSelection(p.Scenarios, o, runs); err != nil {
		return report, err
	}
	mixed := mixedProfiles(runs, o)
	for _, pair := range mixed {
		for _, run := range pair {
			sr := initialScenarioReport(run.Scenario, run.Options)
			sr.BaseScenarioID, sr.ID, sr.ExecutionMode = sr.ID, sr.ID+"_mixed", "mixed"
			sr.Error = "not executed: database preparation did not complete"
			report.Scenarios = append(report.Scenarios, sr)
		}
	}
	report.BinarySHA256, err = executableDigest()
	if err != nil {
		return report, fmt.Errorf("hash executable: %w", err)
	}

	config := makeSQLConfig(o)
	report.Profile.SQLAddress = config.Addr
	root, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return report, err
	}
	defer root.Close()
	root.SetMaxOpenConns(1)
	if err := root.PingContext(ctx); err != nil {
		return report, fmt.Errorf("connect MatrixOne: %w", err)
	}
	versionCtx, cancelVersion := context.WithTimeout(ctx, o.timeout)
	defer cancelVersion()
	if err := root.QueryRowContext(versionCtx, "SELECT VERSION()").Scan(&report.MatrixOneVersion); err != nil {
		return report, fmt.Errorf("read MatrixOne version: %w", err)
	}
	collectSQLServerEnvironment(ctx, root, report.Environment, report.MatrixOneVersion, o.timeout)
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return report, err
	}
	report.Database = fmt.Sprintf("mo_retrieval_bench_%s_%s", time.Now().UTC().Format("20060102_150405"), hex.EncodeToString(nonce[:]))
	if _, err := root.ExecContext(ctx, "CREATE DATABASE `"+report.Database+"`"); err != nil {
		return report, fmt.Errorf("create test database: %w", err)
	}
	report.Cleanup = "pending"
	defer func() {
		if o.keepDB {
			report.Cleanup = "retained"
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), o.timeout)
		defer cancel()
		if _, err := root.ExecContext(cleanupCtx, "DROP DATABASE `"+report.Database+"`"); err != nil {
			report.Cleanup = "failed: " + err.Error()
			report.Errors = append(report.Errors, "cleanup: "+err.Error())
			if runErr == nil {
				runErr = fmt.Errorf("cleanup: %w", err)
			}
		} else {
			report.Cleanup = "dropped"
		}
	}()
	config.DBName = report.Database
	if len(o.queryEndpoints) > 0 {
		report.Profile.QueryEndpoints = append([]string(nil), o.queryEndpoints...)
	} else {
		report.Profile.QueryEndpoints = []string{config.Addr}
	}
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return report, err
	}
	defer db.Close()
	maxConcurrency := 1
	for _, run := range runs {
		maxConcurrency = max(maxConcurrency, run.Options.concurrency)
	}
	db.SetMaxOpenConns(2 * maxConcurrency)
	db.SetMaxIdleConns(2 * maxConcurrency)
	if err := db.PingContext(ctx); err != nil {
		return report, err
	}
	for i, ddl := range p.Manifest.DDL {
		if err := execStage(ctx, db, &report, fmt.Sprintf("ddl_%d", i+1), ddl, o.timeout); err != nil {
			return report, err
		}
	}
	loadedRows := make(map[string]int64)
	for i, load := range p.Manifest.Loads {
		path := p.LoadPaths[i]
		mysql.RegisterLocalFile(path)
		statement := fmt.Sprintf("LOAD DATA LOCAL INFILE %s INTO TABLE `%s` FIELDS TERMINATED BY ',' ENCLOSED BY '\"' LINES TERMINATED BY '\\n'", sqlString(path), load.Table)
		err := execStage(ctx, db, &report, "load_"+load.Table, statement, o.timeout*10)
		mysql.DeregisterLocalFile(path)
		if err != nil {
			return report, err
		}
		var count int64
		countCtx, countCancel := context.WithTimeout(ctx, o.timeout)
		countErr := db.QueryRowContext(countCtx, "SELECT COUNT(*) FROM `"+load.Table+"`").Scan(&count)
		countCancel()
		if countErr != nil {
			return report, countErr
		}
		report.Stages[len(report.Stages)-1].Rows = count
		loadedRows[load.Table] += load.Rows
		if count != loadedRows[load.Table] {
			return report, fmt.Errorf("%s row count: got %d, want %d", load.Table, count, loadedRows[load.Table])
		}
	}
	for i, index := range p.Manifest.Indexes {
		if err := execStage(ctx, db, &report, fmt.Sprintf("index_%d", i+1), index, o.timeout*10); err != nil {
			return report, err
		}
	}
	queryStart = time.Now().UTC()
	// Capture the end before closing sessions and dropping the database. Range
	// requests run only after cleanup, outside all measured query batches.
	defer func() { queryEnd = time.Now().UTC() }()
	for i, run := range runs {
		// All prior workers and leases have finished. Start with fresh server
		// sessions so a previous scenario's SET values cannot become defaults.
		db.SetMaxIdleConns(0)
		db.SetMaxIdleConns(2 * maxConcurrency)
		fmt.Fprintf(os.Stderr, "run %s: client concurrency=%d repeat=%d query-limit=%d\n", run.Scenario.ID, run.Options.concurrency, run.Options.repeat, run.Options.queryLimit)
		sr := runScenario(ctx, db, run.Scenario, run.Options, config)
		report.Scenarios[i] = sr
		if sr.Failures > 0 {
			message := fmt.Sprintf("scenario %s (client concurrency %d): %d failed executions", sr.ID, sr.EffectiveConcurrency, sr.Failures)
			if sr.Error != "" {
				message += ": " + sr.Error
			}
			report.Errors = append(report.Errors, message)
		}
	}
	for i, pair := range mixed {
		db.SetMaxIdleConns(0)
		db.SetMaxIdleConns(2 * maxConcurrency)
		fmt.Fprintf(os.Stderr, "run mixed %s + %s: paired concurrency=%d\n", pair[0].Scenario.ID, pair[1].Scenario.ID, pair[0].Options.concurrency)
		results := runMixedScenarios(ctx, db, pair)
		for route, sr := range results {
			report.Scenarios[len(runs)+2*i+route] = sr
			if sr.Failures > 0 || sr.Error != "" {
				report.Errors = append(report.Errors, fmt.Sprintf("mixed scenario %s: %d failures; %s", sr.ID, sr.Failures, sr.Error))
			}
		}
	}
	if len(report.Errors) > 0 {
		return report, fmt.Errorf("%d benchmark error(s)", len(report.Errors))
	}
	return report, nil
}

func getenv(name string) string { return os.Getenv(name) }

func executableDigest() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func execStage(ctx context.Context, db *sql.DB, report *Report, name, statement string, timeout time.Duration) error {
	fmt.Fprintf(os.Stderr, "stage %s\n", name)
	start := time.Now()
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := db.ExecContext(queryCtx, statement)
	stage := Stage{Name: name, Seconds: time.Since(start).Seconds()}
	if err != nil {
		stage.Error = err.Error()
	}
	report.Stages = append(report.Stages, stage)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func sqlString(value string) string {
	return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(value) + "'"
}

func argsFor(q query, names []string) ([]any, error) {
	args := make([]any, len(names))
	for i, name := range names {
		value, ok := q.Params[name]
		if !ok {
			return nil, fmt.Errorf("query %s lacks %s", q.ID, name)
		}
		switch v := value.(type) {
		case json.Number:
			args[i] = string(v)
		case string, bool, nil:
			args[i] = v
		default:
			return nil, fmt.Errorf("query %s: unsupported parameter %s", q.ID, name)
		}
	}
	return args, nil
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func selectIDs(ctx context.Context, db sqlQueryer, statement string, args []any, topK int, timeout time.Duration, idColumn string, allowDuplicates bool) ([]string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rows, err := db.QueryContext(queryCtx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	idIndex := 0
	if idColumn == "" && len(columns) != 1 {
		return nil, fmt.Errorf("query must return exactly one ID column, got %d", len(columns))
	}
	if idColumn != "" {
		idIndex = -1
		for i, column := range columns {
			if strings.EqualFold(column, idColumn) {
				idIndex = i
				break
			}
		}
		if idIndex < 0 {
			return nil, fmt.Errorf("ID column %q is missing", idColumn)
		}
	}
	ids := make([]string, 0, topK)
	seen := make(map[string]bool, topK)
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		if values[idIndex] == nil {
			return nil, fmt.Errorf("query returned NULL ID")
		}
		id := planValue(values[idIndex])
		if seen[id] && !allowDuplicates {
			return nil, fmt.Errorf("query returned duplicate ID %q", id)
		}
		if len(ids) >= topK {
			return nil, fmt.Errorf("query returned more than top_k=%d rows", topK)
		}
		ids = append(ids, id)
		seen[id] = true
	}
	return ids, rows.Err()
}

func executeQuery(ctx context.Context, db sqlQueryer, s loadedScenario, q query, iteration int, timeout time.Duration) QueryResult {
	result := QueryResult{ID: q.ID, Iteration: iteration}
	start := time.Now()
	var ids []string
	var err error
	if s.Route == "sql" {
		var args []any
		args, err = argsFor(q, s.Args)
		if err == nil {
			ids, err = selectIDs(ctx, db, s.SQL, args, s.TopK, timeout, s.IDColumn, false)
		}
	} else {
		var vecArgs, textArgs []any
		vecArgs, err = argsFor(q, s.VectorArgs)
		if err == nil {
			textArgs, err = argsFor(q, s.FulltextArgs)
		}
		if err == nil {
			var vecIDs, textIDs []string
			var vecErr, textErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				vecIDs, vecErr = selectIDs(ctx, db, s.VectorSQL, vecArgs, s.CandidateK, timeout, "", false)
			}()
			go func() {
				defer wg.Done()
				textIDs, textErr = selectIDs(ctx, db, s.FulltextSQL, textArgs, s.CandidateK, timeout, "", false)
			}()
			wg.Wait()
			if vecErr != nil {
				err = fmt.Errorf("vector route: %w", vecErr)
			} else if textErr != nil {
				err = fmt.Errorf("fulltext route: %w", textErr)
			} else {
				ids = fuseRRF(vecIDs, textIDs, s.TopK)
			}
		}
	}
	result.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.SQLSucceeded = true
	result.IDs = ids
	result.Score, result.Pass, result.Error = scoreQuery(s.scenario, q, ids)
	if s.Oracle == "qrels" {
		metrics := qualityMetrics(s.scenario, q, ids)
		result.Quality = &metrics
	}
	return result
}

func initialScenarioReport(s loadedScenario, o options) ScenarioReport {
	selected := len(s.QueriesData)
	if o.queryLimit > 0 {
		selected = min(selected, o.queryLimit)
	}
	return ScenarioReport{ID: s.ID, Route: s.Route, Oracle: s.Oracle, MinScore: s.MinScore, TopK: s.TopK, CandidateK: s.CandidateK, SQL: s.SQL, VectorSQL: s.VectorSQL, SessionSQL: s.SessionSQL, PlanMustContain: s.PlanContains, SelectedQueries: selected,
		FulltextSQL: s.FulltextSQL, ScenarioSHA256: s.Digest, QueriesSHA256: s.QueryDigest, EffectiveConcurrency: o.concurrency, Repetitions: o.repeat,
		QualityMode: s.QualityMode, NDCGGain: s.NDCGGain, RelevantGrade: s.RelevantGrade, CheckOrder: s.CheckOrder, AllowEmpty: s.AllowEmpty}
}

func runScenario(ctx context.Context, db *sql.DB, s loadedScenario, o options, config *mysql.Config) ScenarioReport {
	report := initialScenarioReport(s, o)
	queries := s.QueriesData
	if o.queryLimit > 0 && len(queries) > o.queryLimit {
		queries = queries[:o.queryLimit]
	}
	report.SelectedQueries = len(queries)
	if len(queries) == 0 {
		report.Failures = 1
		return report
	}
	if len(queries) > 100000/o.repeat || len(queries) > 1000000/o.repeat/s.TopK {
		report.Failures = 1
		report.Error = "result retention limit exceeded: at most 100000 executions and 1000000 result IDs per scenario"
		return report
	}
	if s.Oracle == "stable_multiset" {
		return runStabilityScenario(ctx, s, queries, o, config, report)
	}
	queryers := make([]sqlQueryer, o.concurrency)
	pinned := make([]*sql.Conn, 0, o.concurrency)
	defer func() {
		for _, conn := range pinned {
			_ = conn.Close()
		}
	}()
	for i := range queryers {
		if s.Route == "hybrid_rrf" {
			queryers[i] = db
			continue
		}
		connCtx, cancel := context.WithTimeout(ctx, o.timeout)
		conn, err := db.Conn(connCtx)
		cancel()
		if err != nil {
			report.Failures = 1
			report.Error = fmt.Sprintf("session %d: %v", i, err)
			return report
		}
		pinned = append(pinned, conn)
		for _, statement := range s.SessionSQL {
			setupCtx, setupCancel := context.WithTimeout(ctx, o.timeout)
			_, err = conn.ExecContext(setupCtx, statement)
			setupCancel()
			if err != nil {
				report.Failures = 1
				report.Error = fmt.Sprintf("session %d setup: %v", i, err)
				return report
			}
		}
		queryers[i] = conn
	}
	planSQL, planArgs := s.SQL, s.Args
	if s.Route == "hybrid_rrf" {
		planSQL, planArgs = s.VectorSQL, s.VectorArgs
	}
	if args, err := argsFor(queries[0], planArgs); err == nil {
		var planErr error
		report.Plan, planErr = readQueryPlan(ctx, queryers[0], "EXPLAIN ", planSQL, args, o.timeout)
		if planErr != nil {
			report.PlanError = planErr.Error()
		}
	}
	if s.Route == "hybrid_rrf" {
		if args, err := argsFor(queries[0], s.FulltextArgs); err == nil {
			var planErr error
			report.FulltextPlan, planErr = readQueryPlan(ctx, db, "EXPLAIN ", s.FulltextSQL, args, o.timeout)
			if planErr != nil {
				report.PlanError += " fulltext: " + planErr.Error()
			}
		}
	}
	if s.Route == "sql" && (len(queries[0].AllowedIDRanges) > 0 || len(o.mixedScenarios) > 0) {
		args, err := argsFor(queries[0], s.Args)
		if err == nil {
			var plan string
			plan, err = physicalPlan(ctx, pinned[0], s.SQL, args, o.timeout)
			report.PhysicalPlans = map[string]string{config.Addr: plan}
		}
		if err != nil {
			report.Failures = 1
			report.Error = fmt.Sprintf("physical plan: %v", err)
			return report
		}
	}
	for i := 0; i < o.warmup; i++ {
		_ = executeQuery(ctx, queryers[i%len(queryers)], s, queries[i%len(queries)], -1, o.timeout)
	}
	type job struct{ index, queryIndex, iteration int }
	jobs := make(chan job, o.concurrency)
	report.Results = make([]QueryResult, len(queries)*o.repeat)
	var wg sync.WaitGroup
	start := time.Now()
	for worker := 0; worker < o.concurrency; worker++ {
		queryer := queryers[worker]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if len(o.mixedScenarios) > 0 && s.Route == "sql" {
					report.Results[j.index] = rawSQLQuery(ctx, queryer, s, queries[j.queryIndex], j.iteration, o.timeout)
				} else {
					report.Results[j.index] = executeQuery(ctx, queryer, s, queries[j.queryIndex], j.iteration, o.timeout)
				}
			}
		}()
	}
	for iteration := 0; iteration < o.repeat; iteration++ {
		for queryIndex := range queries {
			jobs <- job{index: iteration*len(queries) + queryIndex, queryIndex: queryIndex, iteration: iteration}
		}
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	if len(o.mixedScenarios) > 0 && s.Route == "sql" {
		for index := range report.Results {
			evaluateRawResult(&report.Results[index], s, queries[index%len(queries)])
		}
	}
	aggregateScenarioResults(&report, elapsed)
	return report
}

func aggregateScenarioResults(report *ScenarioReport, elapsed float64) {
	report.Executions = len(report.Results)
	report.MeasuredSeconds = elapsed
	var sqlSucceeded int
	latencies := make([]float64, 0, len(report.Results))
	for _, result := range report.Results {
		if !result.Pass {
			report.Failures++
			if result.SQLSucceeded {
				report.AssertionFailures++
			}
		}
		if !result.SQLSucceeded {
			report.SQLFailures++
		}
		if result.SQLSucceeded {
			sqlSucceeded++
			latencies = append(latencies, result.LatencyMS)
			report.MeanScore += result.Score
		}
	}
	report.SQLSuccesses = sqlSucceeded
	if elapsed > 0 {
		report.QPS = float64(sqlSucceeded) / elapsed
	}
	if sqlSucceeded > 0 {
		report.MeanScore /= float64(sqlSucceeded)
		setLatencyPercentiles(report, latencies)
	}
}

func setLatencyPercentiles(report *ScenarioReport, latencies []float64) {
	sort.Float64s(latencies)
	report.P50MS = percentile(latencies, 0.50)
	report.P90MS = percentile(latencies, 0.90)
	report.P95MS = percentile(latencies, 0.95)
	report.P99MS = percentile(latencies, 0.99)
}

func planValue(v any) string {
	if bytes, ok := v.([]byte); ok {
		return string(bytes)
	}
	return fmt.Sprint(v)
}

func percentile(sortedValues []float64, p float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	index := int(math.Ceil(p*float64(len(sortedValues)))) - 1
	if index < 0 {
		index = 0
	}
	return sortedValues[index]
}
