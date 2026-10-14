// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMeasurementDrainsInflightAndUsesActualTime(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statement := "SELECT id FROM documents WHERE id = ?"
	mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN " + statement)).WithArgs("a").WillReturnRows(sqlmock.NewRows([]string{"plan"}).AddRow("index scan"))
	for i := 0; i < 2; i++ {
		mock.ExpectQuery(regexp.QuoteMeta(statement)).WithArgs("a").WillDelayFor(20 * time.Millisecond).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("a"))
	}
	s := loadedScenario{scenario: scenario{ID: "vector", Route: "sql", Oracle: "ann_recall", TopK: 1, SQL: statement, Args: []string{"id"}}, QueriesData: []query{{ID: "q", Params: map[string]any{"id": "a"}, ExactIDs: []string{"a"}}}}
	r := runScenario(context.Background(), db, s, options{concurrency: 1, repeat: 1, timeout: time.Second, measureDuration: 5 * time.Millisecond, rounds: 2}, nil)
	if r.PerformanceMode != "duration" || len(r.MeasurementRounds) != 2 || r.Executions != 2 || r.SQLSuccesses != 2 || r.QualitySamples != 2 || r.MeanScore != 1 {
		t.Fatalf("incorrect duration evidence: %+v", r)
	}
	elapsed := 0.0
	for index, round := range r.MeasurementRounds {
		if round.TargetSeconds != 0.005 || round.MeasuredSeconds < 0.015 || round.Executions != 1 || round.QPS != 1/round.MeasuredSeconds || round.StopReason != "deadline" || r.Results[index].Round != index+1 {
			t.Fatalf("nominal duration used or in-flight SQL lost: %+v", round)
		}
		elapsed += round.MeasuredSeconds
	}
	if r.MeasuredSeconds != elapsed || r.QPS != 2/elapsed || db.Stats().InUse != 0 {
		t.Fatalf("wrong pooled denominator or leaked lease: %+v", r)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMeasurementBoundsSamplesAndSeparatesQuality(t *testing.T) {
	p := runMeasurementPhase(context.Background(), 4, time.Second, 8, 2, 2, true, func(worker, index int) []QueryResult {
		r := QueryResult{ID: "q", IDs: []string{"a"}, LatencyMS: 1, SQLSucceeded: index != 3}
		return []QueryResult{r}
	})
	if len(p.records) != 8 || p.stopReason != "sample_limit" || p.errors != 1 {
		t.Fatalf("sample cap failed: %+v", p)
	}
	for index, record := range p.records {
		r := record.results[0]
		if record.index != index || r.QualitySampled != (index < 2) || (len(r.IDs) > 0) != (index < 2) || r.Score != 0 || r.Pass != r.SQLSucceeded {
			t.Fatalf("lost ordering, unbounded IDs, or scored inside timing: %+v", record)
		}
	}
	r := ScenarioReport{PerformanceMode: "duration", Results: []QueryResult{
		{ID: "a", Round: 1, LatencyMS: 10, SQLSucceeded: true, Pass: true, QualitySampled: true, Score: 1},
		{ID: "a", Round: 1, LatencyMS: 20, SQLSucceeded: true, Pass: true},
		{ID: "b", Round: 2, LatencyMS: 30, SQLSucceeded: true, Pass: true, QualitySampled: true, Score: 0},
		{ID: "b", Round: 2, LatencyMS: 1000, Error: "SQL failed"},
	}}
	aggregateScenarioResults(&r, 4)
	if r.QPS != 0.75 || r.MeanScore != 0.5 || r.QualitySamples != 2 || r.P50MS != 20 || r.P99MS != 30 || r.SQLFailures != 1 {
		t.Fatalf("quality/performance sample denominators conflated: %+v", r)
	}
	view, err := makeReportView(Report{Profile: Profile{Concurrency: 1}, Scenarios: []ScenarioReport{r}})
	if err != nil {
		t.Fatal(err)
	}
	if view.Scenarios[0].MeanScore != 0.5 || view.Scenarios[0].QualitySamples != 2 || view.Scenarios[0].P99MS != 30 {
		t.Fatal("renderer included unscored results", view.Scenarios[0])
	}
	chart := buildQueryQualityChart(r)
	if len(chart.Bars) != 2 || chart.Bars[0].Height != 194 || chart.Bars[1].Height != 0 {
		t.Fatal("quality chart included unsampled executions", chart)
	}
}

func TestMeasurementCancellationJoinsWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active atomic.Int64
	p := runMeasurementPhase(ctx, 4, time.Second, 100, 1, 1, true, func(worker, index int) []QueryResult {
		active.Add(1)
		defer active.Add(-1)
		cancel()
		return []QueryResult{{ID: "q", SQLSucceeded: true}}
	})
	if active.Load() != 0 || p.stopReason != "cancelled" || len(p.records) > 4 {
		t.Fatalf("cancellation did not drain workers: %+v", p)
	}
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if waitCooldown(cancelled, time.Hour) {
		t.Fatal("cooldown ignored cancellation")
	}
}

func TestDurationPlannerKeepsFiltersSerialAndBoundsPairedEvidence(t *testing.T) {
	queries := make([]query, 500)
	vector := loadedScenario{scenario: scenario{ID: "vector", Route: "sql", Oracle: "ann_recall", TopK: 100}, QueriesData: queries}
	text := vector
	text.ID, text.Oracle = "fulltext", "qrels"
	filter := vector
	filter.ID = "filter"
	filter.QueriesData = []query{{AllowedIDRanges: [][2]int64{{0, 9}}}}
	stable := vector
	stable.ID, stable.Oracle, stable.MinRepeats = "stable", "stable_multiset", 3
	stable.QueriesData = []query{{ID: "s"}}
	o := options{concurrency: 1, concurrencyLevels: []int{1, 4, 8}, repeat: 1, stabilityRepeat: 30, measureDuration: 30 * time.Second, rounds: 3, mixedScenarios: []string{"vector", "fulltext"}}
	runs, err := planScenarioRuns([]loadedScenario{vector, text, filter, stable}, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMixedSelection([]loadedScenario{vector, text, filter, stable}, o, runs); err != nil {
		t.Fatal(err)
	}
	executions, ids := 0, 0
	count := func(run scenarioRun) {
		if timedPerformance(run.Scenario, run.Options) {
			executions += run.Options.measurementLimit
			ids += min(len(run.Scenario.QueriesData), run.Options.qualityQueryLimit) * 3 * 100
		} else {
			if run.Options.concurrency != 1 {
				t.Fatal("filter/stability admitted concurrent load")
			}
			executions += len(run.Scenario.QueriesData) * run.Options.repeat
			ids += len(run.Scenario.QueriesData) * run.Options.repeat * 100
		}
	}
	for _, run := range runs {
		count(run)
	}
	for _, pair := range mixedProfiles(runs, o) {
		count(pair[0])
		count(pair[1])
	}
	if executions > 100000 || ids > 1000000 {
		t.Fatalf("global retention not bounded: %d requests %d IDs", executions, ids)
	}
}

func TestMeasurementRoundsPoolSamplesAndExposeVariation(t *testing.T) {
	r := ScenarioReport{ID: "vector", Route: "sql", Oracle: "ann_recall", PerformanceMode: "duration", QualityMode: "observe", EffectiveConcurrency: 1, TopK: 1,
		Results:           []QueryResult{{ID: "q", Round: 1, SQLSucceeded: true, Pass: true, QualitySampled: true, Score: 1, LatencyMS: 10}, {ID: "q", Round: 2, SQLSucceeded: true, Pass: true, QualitySampled: true, Score: 1, LatencyMS: 100}},
		MeasurementRounds: []MeasurementRound{{Number: 1, TargetSeconds: 1, MeasuredSeconds: 1, Executions: 1, SQLSuccesses: 1, QPS: 1, P50MS: 10, P99MS: 10, StopReason: "deadline"}, {Number: 2, TargetSeconds: 0.5, MeasuredSeconds: 0.5, Executions: 1, SQLSuccesses: 1, QPS: 2, P50MS: 100, P99MS: 100, StopReason: "deadline"}}}
	aggregateScenarioResults(&r, 1.5)
	summarizeMeasurementRounds(&r)
	if r.QPS != 2/1.5 || r.P99MS != 100 || math.Abs(r.QPSStdDev-math.Sqrt(0.5)) > 1e-9 || len(r.MeasurementWarnings) != 1 {
		t.Fatal("pooled metrics or round variation incorrect", r)
	}
	var html bytes.Buffer
	report := Report{Dataset: "gist1m_full", Status: "passed", Profile: Profile{Concurrency: 1, MeasureDuration: "1s", WarmupDuration: "0s", Rounds: 2}, Scenarios: []ScenarioReport{r}}
	if err := renderReportHTML(&html, report); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"各轮 QPS 柱状图", "变异系数", "质量样本 2", "P50 / P90 / P95 / P99", "目标 / 实测秒数"} {
		if !strings.Contains(html.String(), label) {
			t.Fatalf("HTML omitted %s", label)
		}
	}
	view, err := makeReportView(report)
	if err != nil {
		t.Fatal(err)
	}
	m := terminalModel{width: 100, doc: &terminalDocument{View: view}, percentile: 95}
	lines := strings.Join(m.concurrencyLines(), "\n")
	if !strings.Contains(lines, "各轮吞吐") || !strings.Contains(lines, "变异系数") || !strings.Contains(lines, "质量样本 2") {
		t.Fatal("CLI omitted round evidence", lines)
	}
}

func TestMeasurementLauncherRoundTripAndValidation(t *testing.T) {
	m := launchTestModel(t)
	f := m.launcher
	f.fields[launchMeasureDuration].Value = "45s"
	f.fields[launchWarmupDuration].Value = "2s"
	f.fields[launchRounds].Value = "3"
	f.fields[launchCooldown].Value = "0s"
	request, err := f.request(m.launchSeed)
	if err != nil || request.Options.measureDuration != 45*time.Second || request.Options.warmupDuration != 2*time.Second || request.Options.rounds != 3 || request.Options.cooldown != 0 {
		t.Fatal("measurement options lost", err)
	}
	f.fields[launchMeasureDuration].Value = "-1s"
	if _, err := f.request(m.launchSeed); err == nil {
		t.Fatal("negative measurement admitted")
	}
	f.fields[launchMeasureDuration].Value = "0s"
	if _, err := f.request(m.launchSeed); err != nil {
		t.Fatal("count mode rejected", err)
	}
}

func TestDurationMixedOverlapErrorsAndCleanup(t *testing.T) {
	for _, mode := range []string{"success", "branch_failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			state := &mixedTestState{started: make(chan string, 2), release: make(chan struct{})}
			if mode == "branch_failure" {
				state.failRoute = "T"
			}
			db := sql.OpenDB(mixedTestConnector{state})
			db.SetMaxOpenConns(2)
			db.SetMaxIdleConns(0)
			defer db.Close()
			pair := mixedTestPair()
			for route := range pair {
				pair[route].Scenario.Oracle = "ann_recall"
				pair[route].Options.measureDuration = time.Second
				pair[route].Options.rounds = 1
				pair[route].Options.measurementLimit = 1
				pair[route].Options.qualityQueryLimit = 1
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done := make(chan [2]ScenarioReport, 1)
			go func() { done <- runMixedScenarios(ctx, db, pair) }()
			seen := map[string]bool{}
			for i := 0; i < 2; i++ {
				select {
				case route := <-state.started:
					seen[route] = true
				case <-ctx.Done():
					t.Fatal("duration routes did not overlap")
				}
			}
			if len(seen) != 2 {
				t.Fatal("same route executed twice")
			}
			if mode == "cancel" {
				cancel()
			} else {
				close(state.release)
			}
			var results [2]ScenarioReport
			select {
			case results = <-done:
			case <-time.After(time.Second):
				t.Fatal("duration workers did not drain")
			}
			if db.Stats().InUse != 0 {
				t.Fatal("duration route lease leaked")
			}
			for route, r := range results {
				wantSuccess := mode != "cancel" && (mode != "branch_failure" || route == 0)
				if r.PerformanceMode != "duration" || r.ExecutionMode != "mixed" || r.Executions != 1 || len(r.MeasurementRounds) != 1 || r.MeasuredSeconds != results[0].MeasuredSeconds || (r.SQLSuccesses == 1) != wantSuccess {
					t.Fatalf("duration route evidence incorrect: %+v", r)
				}
				if wantSuccess && (r.MeanScore != 1 || r.QualitySamples != 1 || r.Results[0].ReturnedRows != 1) {
					t.Fatal("lost sampled quality or returned rows", r)
				}
			}
			state.mu.Lock()
			opens, closes := state.opens, state.closes
			state.mu.Unlock()
			if opens != closes {
				t.Fatal("duration route connections did not close", opens, closes)
			}
		})
	}
}

func TestDurationStoredReportKeepsReturnedCountWithoutIDs(t *testing.T) {
	r := ScenarioReport{ID: "vector", Route: "sql", Oracle: "ann_recall", TopK: 1, QualityMode: "observe", PerformanceMode: "duration", EffectiveConcurrency: 1, SelectedQueries: 1,
		Results: []QueryResult{
			{ID: "q", Round: 1, Iteration: 0, SQLSucceeded: true, Pass: true, QualitySampled: true, Score: 1, IDs: []string{"a"}, ReturnedRows: 1, LatencyMS: 1},
			{ID: "q", Round: 1, Iteration: 1, SQLSucceeded: true, Pass: true, ReturnedRows: 1, LatencyMS: 2},
		}, MeasurementRounds: []MeasurementRound{{Number: 1, TargetSeconds: 1, MeasuredSeconds: 1, Executions: 2, SQLSuccesses: 2, QPS: 2, StopReason: "deadline"}}}
	aggregateScenarioResults(&r, 1)
	dir := t.TempDir()
	report := Report{Dataset: "gist1m_full", ToolVersion: "test", StartedAt: time.Now().UTC(), Status: "passed", Cleanup: "dropped", MeasurementMode: "observe", Profile: Profile{Concurrency: 1, Rounds: 1, MeasureDuration: "1s"}, Scenarios: []ScenarioReport{r}}
	if err := writeReport(dir, report); err != nil {
		t.Fatal(err)
	}
	header, err := readTerminalHeader(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loadTerminalDocument(header)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Returned) != 1 || doc.Returned[0] != 1 || doc.View.Scenarios[0].MeanScore != 1 || doc.View.Scenarios[0].QualitySamples != 1 {
		t.Fatal("omitted IDs were interpreted as empty or zero-quality results", doc.Returned, doc.View.Scenarios[0])
	}
	m := terminalModel{width: 100, doc: doc, percentile: 95}
	lines := strings.Join(m.qualityLines(), "\n")
	if !strings.Contains(lines, "平均返回 1.00 条") || !strings.Contains(lines, "覆盖 1/1 条查询") {
		t.Fatal("CLI lost quality evidence", lines)
	}
}
