// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
package main

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

func mixedProfiles(runs []scenarioRun, o options) [][2]scenarioRun {
	if len(o.mixedScenarios) != 2 {
		return nil
	}
	var pairs [][2]scenarioRun
	for _, run := range runs {
		if run.Scenario.ID != o.mixedScenarios[0] {
			continue
		}
		for _, other := range runs {
			if other.Scenario.ID == o.mixedScenarios[1] && other.Options.concurrency == run.Options.concurrency {
				pairs = append(pairs, [2]scenarioRun{run, other})
			}
		}
	}
	return pairs
}

func validateMixedSelection(scenarios []loadedScenario, o options, runs []scenarioRun) error {
	if len(o.mixedScenarios) == 0 {
		return nil
	}
	if len(o.mixedScenarios) != 2 || o.mixedScenarios[0] == o.mixedScenarios[1] {
		return fmt.Errorf("--mixed-scenarios requires two distinct ordinary SQL scenario IDs")
	}
	for _, id := range o.mixedScenarios {
		found := false
		for _, s := range scenarios {
			if s.ID == id {
				found = s.Route == "sql" && s.Oracle != "stable_multiset" && len(s.ID) <= 57
			}
			if s.ID == id+"_mixed" {
				return fmt.Errorf("mixed scenario ID collides with %s", s.ID)
			}
		}
		if !found {
			return fmt.Errorf("mixed scenario %q must identify an ordinary SQL scenario", id)
		}
	}
	pairs := mixedProfiles(runs, o)
	if len(pairs) == 0 {
		return fmt.Errorf("no mixed profiles selected")
	}
	var executions, ids int
	countRun := func(run scenarioRun) int {
		n := len(run.Scenario.QueriesData)
		if run.Options.queryLimit > 0 {
			n = min(n, run.Options.queryLimit)
		}
		executions += n * run.Options.repeat
		ids += n * run.Options.repeat * run.Scenario.TopK
		return n
	}
	for _, run := range runs {
		countRun(run)
	}
	for _, pair := range pairs {
		a, b := countRun(pair[0]), countRun(pair[1])
		if a != b || pair[0].Options.repeat != pair[1].Options.repeat {
			return fmt.Errorf("mixed profiles require equal selected query counts and repetitions")
		}
	}
	if executions > 100000 || ids > 1000000 {
		return fmt.Errorf("isolated plus mixed profiles exceed 100000 executions or 1000000 retained IDs")
	}
	return nil
}

func rawSQLQuery(ctx context.Context, conn sqlQueryer, s loadedScenario, q query, iteration int, timeout time.Duration) QueryResult {
	r := QueryResult{ID: q.ID, Iteration: iteration}
	start := time.Now()
	args, err := argsFor(q, s.Args)
	if err == nil {
		r.IDs, err = selectIDs(ctx, conn, s.SQL, args, s.TopK, timeout, s.IDColumn, false)
	}
	r.LatencyMS = float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		r.Error = err.Error()
	} else {
		r.SQLSucceeded = true
	}
	return r
}

func evaluateRawResult(r *QueryResult, s loadedScenario, q query) {
	if !r.SQLSucceeded {
		return
	}
	r.Score, r.Pass, r.Error = scoreQuery(s.scenario, q, r.IDs)
	if s.Oracle == "qrels" {
		metrics := qualityMetrics(s.scenario, q, r.IDs)
		r.Quality = &metrics
	}
}

func runMixedScenarios(ctx context.Context, db *sql.DB, pair [2]scenarioRun) (reports [2]ScenarioReport) {
	var queries [2][]query
	var sessions [2][]*sql.Conn
	for route, run := range pair {
		r := initialScenarioReport(run.Scenario, run.Options)
		r.BaseScenarioID, r.ID, r.ExecutionMode = r.ID, r.ID+"_mixed", "mixed"
		reports[route] = r
		queries[route] = run.Scenario.QueriesData[:r.SelectedQueries]
	}
	defer func() {
		for _, route := range sessions {
			for _, conn := range route {
				_ = conn.Close()
			}
		}
	}()
	fail := func(err error) [2]ScenarioReport {
		for route := range reports {
			reports[route].Error = err.Error()
			reports[route].Failures = 1
		}
		return reports
	}
	o := pair[0].Options
	if reports[0].SelectedQueries == 0 || reports[0].SelectedQueries != reports[1].SelectedQueries || o.repeat < 1 || o.concurrency < 1 {
		return fail(fmt.Errorf("invalid mixed runtime profile"))
	}
	for route, run := range pair {
		for worker := 0; worker < o.concurrency; worker++ {
			setupCtx, cancel := context.WithTimeout(ctx, o.timeout)
			conn, err := db.Conn(setupCtx)
			cancel()
			if err != nil {
				return fail(fmt.Errorf("route %s session %d: %w", run.Scenario.ID, worker, err))
			}
			sessions[route] = append(sessions[route], conn)
			for _, statement := range run.Scenario.SessionSQL {
				setupCtx, cancel = context.WithTimeout(ctx, o.timeout)
				_, err = conn.ExecContext(setupCtx, statement)
				cancel()
				if err != nil {
					return fail(fmt.Errorf("route %s setup: %w", run.Scenario.ID, err))
				}
			}
		}
		args, err := argsFor(queries[route][0], run.Scenario.Args)
		if err != nil {
			return fail(err)
		}
		reports[route].Plan, err = readQueryPlan(ctx, sessions[route][0], "EXPLAIN ", run.Scenario.SQL, args, o.timeout)
		if err != nil {
			return fail(fmt.Errorf("route %s explain: %w", run.Scenario.ID, err))
		}
		physical, err := physicalPlan(ctx, sessions[route][0], run.Scenario.SQL, args, o.timeout)
		if err != nil {
			return fail(fmt.Errorf("route %s physical plan: %w", run.Scenario.ID, err))
		}
		reports[route].PhysicalPlans = map[string]string{fmt.Sprintf("%s:%d", o.host, o.port): physical}
	}
	executePair := func(worker, index, iteration int) [2]QueryResult {
		var results [2]QueryResult
		var children sync.WaitGroup
		children.Add(2)
		for route := range pair {
			go func(route int) {
				defer children.Done()
				results[route] = rawSQLQuery(ctx, sessions[route][worker], pair[route].Scenario, queries[route][index], iteration, o.timeout)
			}(route)
		}
		children.Wait()
		return results
	}
	for i := 0; i < o.warmup; i++ {
		for route, result := range executePair(i%o.concurrency, i%len(queries[0]), -1) {
			if !result.SQLSucceeded {
				return fail(fmt.Errorf("route %s warmup: %s", pair[route].Scenario.ID, result.Error))
			}
		}
	}
	n := len(queries[0]) * o.repeat
	for route := range reports {
		reports[route].Results = make([]QueryResult, n)
	}
	jobs := make(chan int, o.concurrency)
	var workers sync.WaitGroup
	start := time.Now()
	for worker := 0; worker < o.concurrency; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for index := range jobs {
				results := executePair(worker, index%len(queries[0]), index/len(queries[0]))
				for route := range reports {
					reports[route].Results[index] = results[route]
				}
			}
		}(worker)
	}
	for index := 0; index < n; index++ {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	elapsed := time.Since(start).Seconds()
	for route := range reports {
		for index := range reports[route].Results {
			r := &reports[route].Results[index]
			if !r.SQLSucceeded {
				continue
			}
			q := queries[route][index%len(queries[route])]
			evaluateRawResult(r, pair[route].Scenario, q)
		}
		aggregateScenarioResults(&reports[route], elapsed)
	}
	return reports
}
