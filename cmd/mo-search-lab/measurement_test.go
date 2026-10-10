// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeasurementsPreserveQualityWithoutAcceptanceFailures(t *testing.T) {
	for _, test := range []struct {
		name         string
		scene        scenario
		truth        query
		ids          []string
		score        float64
		observations bool
	}{
		{"low recall", scenario{Oracle: "ann_recall", TopK: 2, MinScore: .7}, query{ExactIDs: []string{"1", "2"}}, []string{"1", "3"}, .5, false},
		{"empty recall", scenario{Oracle: "ann_recall", TopK: 2, MinScore: .7}, query{ExactIDs: []string{"1", "2"}}, nil, 0, false},
		{"filter difference", scenario{Oracle: "ann_recall", TopK: 2, MinScore: .7}, query{ExactIDs: []string{"1", "2"}, AllowedIDRanges: [][2]int64{{1, 2}}}, []string{"1", "3"}, .5, true},
		{"zero relevance", scenario{Oracle: "qrels", TopK: 2, MinScore: .8}, query{Relevance: map[string]int{"1": 2}}, []string{"3"}, 0, false},
		{"exact mismatch", scenario{Oracle: "exact_ids", TopK: 2}, query{ExactIDs: []string{"1", "2"}}, []string{"2", "1"}, 0, true},
		{"empty functional", scenario{Oracle: "nonempty", TopK: 2}, query{}, nil, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := loadedScenario{scenario: test.scene, QueriesData: []query{test.truth}}
			r := QueryResult{SQLSucceeded: true, IDs: test.ids}
			evaluateRawResult(&r, s, test.truth)
			if !r.Pass || r.Error != "" || r.Score != test.score || (len(r.Observations) > 0) != test.observations {
				t.Fatalf("measurement lost quality or became a failure: %+v", r)
			}
			report := initialScenarioReport(s, options{concurrency: 1, repeat: 1})
			report.Results = []QueryResult{r, {Error: "query timeout"}}
			aggregateScenarioResults(&report, 1)
			if report.SQLSuccesses != 1 || report.SQLFailures != 1 || report.Failures != 1 || report.AssertionFailures != 0 || report.MeanScore != test.score || report.QualityMode != "observe" || report.MinScore != 0 {
				t.Fatalf("execution errors or observations counted incorrectly: %+v", report)
			}
			if s.MinScore != test.scene.MinScore || s.QualityMode != test.scene.QualityMode {
				t.Fatal("frozen pack was changed")
			}
		})
	}
}

func observedStabilityFixture() ScenarioReport {
	s := loadedScenario{scenario: scenario{ID: "stable", Route: "sql", Oracle: "stable_multiset", TopK: 2, ExpectedRows: 2}, QueriesData: []query{{ID: "q", ExactIDs: []string{"1", "2"}, AllowedIDRanges: [][2]int64{{1, 2}}}}}
	r := initialScenarioReport(s, options{concurrency: 1, repeat: 5})
	r.Results = []QueryResult{
		{ID: "q", Iteration: 0, IDs: []string{"1", "2"}, SQLSucceeded: true},
		{ID: "q", Iteration: 1, IDs: []string{"2", "1"}, SQLSucceeded: true},
		{ID: "q", Iteration: 2, IDs: []string{"1", "3"}, SQLSucceeded: true},
		{ID: "q", Iteration: 3, SQLSucceeded: true},
		{ID: "q", Iteration: 4, Error: "query timeout"},
	}
	evaluateStability(&r, s.QueriesData, 5, s.ExpectedRows)
	aggregateScenarioResults(&r, 1)
	return r
}

func TestObservedStabilityKeepsChangesAndOnlyFailsOnExecutionErrors(t *testing.T) {
	r := observedStabilityFixture()
	q := r.Stability[0]
	if q.DistinctResults != 3 || q.DistinctOrders != 4 || q.ReorderedExecutions != 1 || q.WorstOverlap != 0 || q.MaxChangedIDs != 2 || q.Failures != 1 || r.AssertionFailures != 0 || r.SQLFailures != 1 || r.Failures != 1 {
		t.Fatalf("lost changes or fabricated failure: %+v %+v", r, q)
	}
	for _, result := range r.Results[:4] {
		if !result.Pass || result.Error != "" {
			t.Fatal("observation failed", result)
		}
	}
	matrix := buildStabilityMatrix(r)
	for i, class := range []string{"same", "assertion", "assertion", "assertion", "sql-error"} {
		if matrix.Rows[0].Cells[i].Class != class {
			t.Fatal("change disappeared from matrix", matrix.Rows[0].Cells)
		}
	}
	if !matrix.Observed || strings.Contains(matrix.Rows[0].Cells[1].Tooltip, "断言") {
		t.Fatal("matrix still describes acceptance assertions")
	}
}

func TestObservedReportShowsChangesWithoutAcceptanceLabels(t *testing.T) {
	r := terminalFixture("observe_report")
	r.MeasurementMode, r.Status, r.Cleanup = "observe", "failed", "dropped"
	r.Errors = []string{"SQL query timeout"}
	r.Profile.ConcurrencyLevels = []int{1}
	s := loadedScenario{scenario: scenario{ID: "recall", Route: "sql", Oracle: "ann_recall", TopK: 2, MinScore: .7}, QueriesData: []query{{ID: "q", ExactIDs: []string{"1", "2"}}}}
	scene := initialScenarioReport(s, options{concurrency: 1, repeat: 1})
	result := QueryResult{ID: "q", SQLSucceeded: true, IDs: []string{"1", "3"}, LatencyMS: 3}
	evaluateRawResult(&result, s, s.QueriesData[0])
	scene.Results = []QueryResult{result}
	aggregateScenarioResults(&scene, 1)
	r.Scenarios = []ScenarioReport{scene, observedStabilityFixture()}
	dir := t.TempDir()
	if err := writeReport(dir, r); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"断言", "单查询阈值", "未通过"} {
		if strings.Contains(string(body), old) {
			t.Fatalf("observed report retains %s", old)
		}
	}
	for _, wanted := range []string{"运行有错误", "平均 Recall@2", "变化 / 结果差异", "顺序变化次数", "观察到变化或结果差异"} {
		if !strings.Contains(string(body), wanted) {
			t.Fatal("report omitted", wanted)
		}
	}
}
