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
	"bytes"
	"strings"
	"testing"
)

func TestQueryQualityDistinguishesZeroScoresFromMissingSQL(t *testing.T) {
	s := ScenarioReport{ID: "text", Oracle: "qrels", TopK: 10, MinScore: .3, Results: []QueryResult{
		{ID: "zero<script>", SQLSucceeded: true, Score: 0},
		{ID: "missing", Error: "SQL failed", Score: 1},
		{ID: "repeat", SQLSucceeded: true, Score: 1, Pass: true},
		{ID: "repeat", SQLSucceeded: true, Score: 0},
	}}
	chart := buildQueryQualityChart(s)
	if len(chart.Bars) != 3 || !chart.Bars[0].HasSample || chart.Bars[0].Height != 0 || chart.Bars[1].HasSample || chart.Bars[2].Height != 97 || chart.Bars[2].Class != "quality-fail" {
		t.Fatalf("zero, missing or repeated quality scores were conflated: %+v", chart)
	}
	if !strings.Contains(chart.Bars[1].Tooltip, "无成功执行样本") || chart.Threshold != .3 || chart.ThresholdY != 159.8 {
		t.Fatal("missing execution was scored or quality threshold changed")
	}
	var html bytes.Buffer
	if err := renderReportHTML(&html, Report{Dataset: "text", Status: "failed", Scenarios: []ScenarioReport{s}}); err != nil || !strings.Contains(html.String(), "zero&lt;script&gt;") || strings.Contains(html.String(), "<script>") {
		t.Fatalf("quality chart labels were not escaped: %v", err)
	}
}

func TestChartOverviewUsesMeasuredValuesAndSeparatesQualityMetrics(t *testing.T) {
	// Equal scoring parameters do not identify distinct SQL/input scenarios.
	left := overviewLabel(concurrencySeries{ID: "raw_text", SessionSQL: []string{"SET ft_relevancy_algorithm = 'TF-IDF'"}})
	right := overviewLabel(concurrencySeries{ID: "tokenized_text", SessionSQL: []string{"SET ft_relevancy_algorithm = 'TF-IDF'"}})
	if left == right || !strings.Contains(left, "raw_text") || !strings.Contains(right, "tokenized_text") {
		t.Fatal("overview conflated distinct scenarios sharing session parameters")
	}
	report := Report{Dataset: "plots", Status: "failed", Profile: Profile{ConcurrencyLevels: []int{1, 4, 8}}}
	for _, level := range []int{1, 4, 8} {
		s := ScenarioReport{ID: "vector", Oracle: "ann_recall", TopK: 10, EffectiveConcurrency: level, QPS: 10, Repetitions: 1,
			Results: []QueryResult{{ID: "q", SQLSucceeded: true, Pass: false, Score: .6, LatencyMS: 50, IDs: []string{"a"}}}}
		if level == 4 {
			s.Results[0].IDs = []string{"b"}
		}
		if level == 8 {
			s.Results[0] = QueryResult{ID: "q", Error: "SQL failed", LatencyMS: 10000, Score: 1}
		}
		report.Scenarios = append(report.Scenarios, s)
	}
	report.Scenarios = append(report.Scenarios, ScenarioReport{ID: "text", Oracle: "qrels", TopK: 10, EffectiveConcurrency: 1, QPS: 5,
		Results: []QueryResult{{ID: "text-q", SQLSucceeded: true, Pass: true, Score: .8, LatencyMS: 20}}})
	view, err := makeReportView(report)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Overview.Enabled || len(view.Overview.Qualities) != 2 || view.Overview.Qualities[0].Title != "平均 Recall@10" || view.Overview.Qualities[1].Title != "平均 nDCG@10" {
		t.Fatalf("unlike quality oracles were combined: %+v", view.Overview)
	}
	line := view.Overview.QPS.Lines[0]
	if len(line.Points) != 2 || line.Points[0].Value != 10 || line.Points[1].Value != 10 || view.Overview.Qualities[0].Lines[0].Points[0].Value != .6 || view.Overview.Latencies[2].Lines[0].Points[0].Value != 50 {
		t.Fatalf("SQL errors or quality failures changed sample selection: %+v", view.Overview)
	}
	if view.Overview.Qualities[1].Lines[0].Class != view.Overview.QPS.Lines[1].Class {
		t.Fatal("scenario colors were lost")
	}
	if report.Scenarios[0].MeanScore != 0 {
		t.Fatal("view reconstruction mutated the saved mean")
	}
	var html bytes.Buffer
	if err := renderReportHTML(&html, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="overview"`, `id="overview-p95" checked`, `data-value="0.600000"`, "均值不代替单查询断言", "运行参数、数据准备与测量身份"} {
		if !strings.Contains(html.String(), want) {
			t.Fatalf("chart-first report missing %q", want)
		}
	}
	if strings.Index(html.String(), `id="overview"`) > strings.Index(html.String(), "<table>") {
		t.Fatal("numeric tables still precede primary plots")
	}
	// A missing middle level is a gap, rather than a connection or measured zero.
	copy := view.ConcurrencySeries[0]
	copy.Points = append([]concurrencyPoint(nil), copy.Points...)
	copy.Points[1].HasSamples, copy.Points[2].HasSamples = false, true
	chart := makeOverviewChart("gap", "gap", "QPS", []concurrencySeries{copy}, 0, func(p concurrencyPoint) (float64, string, bool) { return p.QPS, "", p.HasSamples })
	if strings.Contains(chart.Lines[0].Path, "L") || strings.Count(chart.Lines[0].Path, "M") != 2 {
		t.Fatal("overview bridged an unmeasured level")
	}
	report.Scenarios[0].Results[0].Score = -1
	if _, err := makeReportView(report); err == nil {
		t.Fatal("invalid quality was plotted")
	}
}

func TestStabilityMatrixShowsErrorsMissingExecutionsAndBoundedBins(t *testing.T) {
	s := ScenarioReport{Repetitions: 4, Stability: []StabilityResult{{QueryID: "q<script>"}}, Results: []QueryResult{
		{ID: "q<script>", Iteration: 0, SQLSucceeded: true, Pass: true},
		{ID: "q<script>", Iteration: 1, SQLSucceeded: true, Pass: false},
		{ID: "q<script>", Iteration: 2, Error: "timeout"},
	}}
	grid := buildStabilityMatrix(s)
	if !grid.HasData || grid.Columns != 4 || len(grid.Rows) != 1 {
		t.Fatalf("bad matrix dimensions: %+v", grid)
	}
	for i, state := range []string{"same", "assertion", "sql-error", "missing"} {
		if grid.Rows[0].Cells[i].Class != state {
			t.Fatalf("cell %d masked an incomplete/failed execution: %+v", i, grid.Rows[0].Cells[i])
		}
	}
	s.Repetitions = 120
	grid = buildStabilityMatrix(s)
	if grid.Columns != 60 || grid.BinSize != 2 || grid.Rows[0].Cells[0].Class != "assertion" || grid.Rows[0].Cells[1].Class != "sql-error" || !strings.Contains(grid.Rows[0].Cells[0].Tooltip, "第 1 至 2 次") {
		t.Fatal("binning hid failures or lost execution ranges")
	}
	s.Results = nil
	if buildStabilityMatrix(s).HasData {
		t.Fatal("unexecuted scenario created measured cells")
	}
	s.Repetitions = 1
	for i := 0; i < 31; i++ {
		id := strings.Repeat("q", i+1)
		s.Stability = append(s.Stability, StabilityResult{QueryID: id})
		s.Results = append(s.Results, QueryResult{ID: id, SQLSucceeded: true, Pass: true})
	}
	grid = buildStabilityMatrix(s)
	if len(grid.Rows) != 30 || grid.HiddenQueries != 2 {
		t.Fatal("visible-query bound discarded its hidden count")
	}
	var html bytes.Buffer
	if err := renderReportHTML(&html, Report{Dataset: "matrix", Status: "passed", Scenarios: []ScenarioReport{{ID: "stable", Oracle: "stable_multiset", Repetitions: 1, Stability: []StabilityResult{{QueryID: "q<script>"}}, Results: []QueryResult{{ID: "q<script>", SQLSucceeded: true, Pass: true}}}}}); err != nil || !strings.Contains(html.String(), "q&lt;script&gt;") || strings.Contains(html.String(), "<script>") {
		t.Fatalf("matrix labels/tooltips did not escape input: %v", err)
	}
}
