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
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAggregateScenarioResultsIncludesQualityFailures(t *testing.T) {
	report := ScenarioReport{Results: []QueryResult{
		{LatencyMS: 10, Score: 1, Pass: true, SQLSucceeded: true},
		{LatencyMS: 20, Score: 0, Pass: false, SQLSucceeded: true, Error: "below quality threshold"},
		{LatencyMS: 30, Score: 0, Pass: false, Error: "SQL failed"},
	}}
	aggregateScenarioResults(&report, 1)
	if report.Executions != 3 || report.Failures != 2 {
		t.Fatalf("executions=%d failures=%d, want 3 and 2", report.Executions, report.Failures)
	}
	if report.MeanScore != 0.5 || report.P50MS != 10 || report.P95MS != 20 || report.QPS != 2 {
		t.Fatalf("mean=%v p50=%v p95=%v qps=%v, want 0.5, 10, 20, 2",
			report.MeanScore, report.P50MS, report.P95MS, report.QPS)
	}
}

func TestAggregateScenarioResultsAllQualityFailures(t *testing.T) {
	report := ScenarioReport{Results: []QueryResult{
		{LatencyMS: 3, SQLSucceeded: true, Error: "empty result"},
		{LatencyMS: 5, SQLSucceeded: true, Error: "empty result"},
	}}
	aggregateScenarioResults(&report, 1)
	if report.Failures != 2 || report.MeanScore != 0 || report.P95MS != 5 || report.QPS != 2 {
		t.Fatalf("failures=%d mean=%v p95=%v qps=%v, want 2, 0, 5, 2",
			report.Failures, report.MeanScore, report.P95MS, report.QPS)
	}
}

func TestAggregateScenarioResultsPercentileRanks(t *testing.T) {
	var report ScenarioReport
	for latency := 100; latency > 0; latency-- {
		report.Results = append(report.Results, QueryResult{LatencyMS: float64(latency), SQLSucceeded: true, Pass: latency < 99})
	}
	report.Results = append(report.Results, QueryResult{LatencyMS: 10000, Error: "SQL failed"})
	aggregateScenarioResults(&report, 2)
	if report.P50MS != 50 || report.P90MS != 90 || report.P95MS != 95 || report.P99MS != 99 || report.QPS != 50 || report.Failures != 3 {
		t.Fatalf("wrong percentile ranks or SQL sample selection: %+v", report)
	}
	if report.SQLSuccesses != 100 || report.SQLFailures != 1 || report.AssertionFailures != 2 || report.MeasuredSeconds != 2 {
		t.Fatalf("SQL errors and assertion failures were conflated: %+v", report)
	}
}

func TestConcurrentScenarioRetainsObservationsAndClosesSessions(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		statements []string
	}{{"server_defaults", nil}, {"session_parameters", []string{"SET probe_limit=5"}}} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			db.SetMaxOpenConns(8)
			db.SetMaxIdleConns(8)
			mock.MatchExpectationsInOrder(false)
			statement := "SELECT id FROM documents WHERE id = ?"
			for i := 0; i < 4; i++ {
				for _, setup := range testCase.statements {
					mock.ExpectExec(regexp.QuoteMeta(setup)).WillReturnResult(sqlmock.NewResult(0, 0))
				}
			}
			mock.ExpectQuery(regexp.QuoteMeta("EXPLAIN " + statement)).WithArgs("a").WillReturnRows(sqlmock.NewRows([]string{"plan"}).AddRow("index scan"))
			for _, id := range []string{"a", "b"} {
				for i := 0; i < 3; i++ {
					mock.ExpectQuery(regexp.QuoteMeta(statement)).WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(id))
				}
			}
			s := loadedScenario{scenario: scenario{ID: "concurrent", Route: "sql", Oracle: "exact_ids", TopK: 1, SQL: statement, Args: []string{"id"}, SessionSQL: testCase.statements},
				QueriesData: []query{{ID: "a", Params: map[string]any{"id": "a"}, ExactIDs: []string{"a"}}, {ID: "b", Params: map[string]any{"id": "b"}, ExactIDs: []string{"b"}}}}
			report := runScenario(context.Background(), db, s, options{concurrency: 4, repeat: 2, warmup: 2, timeout: time.Second}, nil)
			if report.Executions != 4 || report.SQLSuccesses != 4 || report.Failures != 0 || report.EffectiveConcurrency != 4 || report.Repetitions != 2 || report.QPS != 4/report.MeasuredSeconds {
				t.Fatalf("bad concurrent observations: %+v", report)
			}
			for i, result := range report.Results {
				if result.ID != s.QueriesData[i%2].ID || result.Iteration != i/2 || !result.Pass {
					t.Fatalf("lost or misplaced observation %d: %+v", i, result)
				}
			}
			if db.Stats().InUse != 0 || db.Stats().OpenConnections != 4 {
				t.Fatal("scenario did not prepare all worker connections or retained a lease")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("session_setup_failure", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		mock.ExpectExec("SET probe_limit=5").WillReturnError(fmt.Errorf("setup rejected"))
		s := loadedScenario{scenario: scenario{ID: "bad_session", Route: "sql", TopK: 1, SessionSQL: []string{"SET probe_limit=5"}}, QueriesData: []query{{ID: "q"}}}
		report := runScenario(context.Background(), db, s, options{concurrency: 4, repeat: 1, timeout: time.Second}, nil)
		if report.Failures != 1 || len(report.Results) != 0 || db.Stats().InUse != 0 {
			t.Fatalf("partial setup leaked a lease or started queries: %+v", report)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCancelledSetupPublishesUnexecutedProfiles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // No network/database fixture: admission is already cancelled.
	ordinary := loadedScenario{scenario: scenario{ID: "recall", Route: "sql", Oracle: "ann_recall", TopK: 10}, QueriesData: []query{{ID: "q"}}}
	stable := ordinary
	stable.ID, stable.Oracle, stable.MinRepeats = "stable", "stable_multiset", 3
	p := &pack{Manifest: manifest{Dataset: "cancelled_setup"}, Scenarios: []loadedScenario{ordinary, stable}}
	report, err := runBenchmark(ctx, p, options{host: "127.0.0.1", port: 1, repeat: 1, concurrency: 1, concurrencyLevels: []int{1, 4}, stabilityRepeat: 3, timeout: time.Second})
	if !errors.Is(err, context.Canceled) || report.Status != "failed" || report.Cleanup != "not_started" || len(report.Scenarios) != 3 {
		t.Fatalf("setup failure hid planned checks: %+v %v", report, err)
	}
	for _, s := range report.Scenarios {
		if s.Error == "" || s.Executions != 0 || s.SelectedQueries != 1 || s.SQLSuccesses != 0 || len(s.Results) != 0 {
			t.Fatalf("unexecuted profile fabricated measurements: %+v", s)
		}
	}
	view, err := makeReportView(report)
	if err != nil || len(view.ConcurrencySeries) != 1 || view.ConcurrencySeries[0].HasSamples || view.HasLatencySamples {
		t.Fatalf("unexecuted checks generated curves: %+v %v", view, err)
	}
}
