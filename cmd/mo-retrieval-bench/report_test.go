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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportEscapesDataAndWritesBothFormats(t *testing.T) {
	dir := t.TempDir()
	if err := writeReport(dir, Report{Dataset: "<script>alert(1)</script>", Status: "failed", Scenarios: []ScenarioReport{{ID: "unstable", Oracle: "stable_multiset", Stability: []StabilityResult{{QueryID: "q", DistinctResults: 2}}, MaxDistinctResults: 2, WorstOverlap: 0.5, MaxChangedIDs: 200}}}); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<script>alert(1)</script>") || !strings.Contains(string(html), "&lt;script&gt;") {
		t.Fatal("HTML report did not escape data")
	}
	if !strings.Contains(string(html), "最多不同结果集合") || !strings.Contains(string(html), "200") {
		t.Fatal("HTML report omitted stability evidence")
	}
	if _, err := os.Stat(filepath.Join(dir, "report.json")); err != nil {
		t.Fatal(err)
	}
	if err := ensureReportTarget(dir); err == nil {
		t.Fatal("existing report should be rejected before a benchmark run")
	}
	if err := writeReport(dir, Report{Dataset: "overwrite"}); err == nil {
		t.Fatal("existing report was overwritten")
	}
}

func TestRenderSavedReportPreservesMeasurements(t *testing.T) {
	dir := t.TempDir()
	// Legacy report has no p90_ms. A successful SQL quality failure must still
	// contribute; the much slower SQL error must not affect the shared axis.
	raw := []byte(`{"dataset":"legacy","status":"failed","binary_sha256":"frozen-binary","tool_version":"measured-version","scenarios":[{"id":"slow<script>","results":[{"latency_ms":20,"sql_succeeded":true,"pass":false},{"latency_ms":10000,"sql_succeeded":false}]},{"id":"fast","results":[{"latency_ms":10,"sql_succeeded":true,"pass":true}]},{"id":"empty","results":[{"latency_ms":1,"sql_succeeded":false}]}]}`)
	jsonPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(jsonPath, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := renderSavedReport(dir); err != nil {
		t.Fatal(err)
	}
	htmlPath := filepath.Join(dir, "report.html")
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"查询延迟分位数", "P90 20.00 ms", "P95 20.00 ms", "P99 20.00 ms", `width="320.00"`, `width="640.00"`, "没有成功 SQL 样本", "slow&lt;script&gt;", "measured-version", "frozen-binary"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("report missing %q", want)
		}
	}
	if strings.Contains(string(html), "slow<script>") {
		t.Fatal("chart label was not escaped")
	}
	after, err := os.ReadFile(jsonPath)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatalf("render changed original measurements: %v", err)
	}
	for _, invalid := range []string{`{`, `{}`, `{"dataset":"legacy","status":"passed","scenarios":[{"id":"bad","results":[{"latency_ms":-1,"sql_succeeded":true}]}]}`} {
		if err := os.WriteFile(jsonPath, []byte(invalid), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := renderSavedReport(dir); err == nil {
			t.Fatal("invalid report rendered successfully")
		}
		after, err := os.ReadFile(htmlPath)
		if err != nil || !bytes.Equal(html, after) {
			t.Fatalf("render failure changed previous HTML: %v", err)
		}
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".report-*.html"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary HTML was not cleaned up: %v %v", leftovers, err)
	}
}

func TestConcurrencyAndStabilityReport(t *testing.T) {
	report := Report{Dataset: "gist", Status: "failed", Profile: Profile{Concurrency: 1, ConcurrencyLevels: []int{1, 4, 8}}}
	for i, level := range []int{1, 4, 8} {
		s := ScenarioReport{ID: "load<script>", Oracle: "ann_recall", TopK: 10, EffectiveConcurrency: level, Repetitions: 1, SelectedQueries: 3, ScenarioSHA256: "scenario", QueriesSHA256: "queries", QPS: []float64{0.5, 0.8, 0}[i],
			Results: []QueryResult{{ID: "q1", SQLSucceeded: true, Pass: true, LatencyMS: 0.2}, {ID: "q2", SQLSucceeded: true, Error: "bad recall", LatencyMS: 0.4}, {ID: "q3", Error: "SQL error", LatencyMS: 10000}}}
		if i == 2 {
			s.Results = s.Results[2:]
		}
		report.Scenarios = append(report.Scenarios, s)
	}
	stable := ScenarioReport{ID: "stable", Oracle: "stable_multiset", EffectiveConcurrency: 1, Repetitions: 3, SelectedQueries: 1, PhysicalPlans: map[string]string{"cn:6001": "Remote<script>"}, Results: []QueryResult{
		{ID: "q", SQLSucceeded: true, IDs: []string{"a", "b"}},
		{ID: "q", SQLSucceeded: true, IDs: []string{"b", "a"}},
		{ID: "q", SQLSucceeded: true, IDs: []string{"a", "c"}},
	}}
	evaluateStability(&stable, []query{{ID: "q"}}, 3, 2)
	stable.Stability[0].SQLSuccesses = 0 // missing legacy display counters
	report.Scenarios = append(report.Scenarios, stable)
	view, err := makeReportView(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.ConcurrencySeries) != 1 || len(view.ConcurrencySeries[0].Points) != 3 || len(view.StabilityScenarios) != 1 {
		t.Fatalf("load profiles and stability checks were mixed: %+v", view)
	}
	group := view.ConcurrencySeries[0]
	if group.QPSMaximum != 0.8 || group.LatencyMaximum != 0.4 || group.Points[1].QPSY != 26 || group.Points[1].P99Y != 26 || group.Points[2].HasSamples || group.Points[0].SQLFailures != 1 || group.Points[0].AssertionFailures != 1 {
		t.Fatalf("incorrect scale, samples or counters: %+v", group)
	}
	if view.StabilityScenarios[0].Stability[0].SQLSuccesses != 3 || view.StabilityScenarios[0].Stability[0].Failures != 1 || report.Scenarios[3].Stability[0].SQLSuccesses != 0 {
		t.Fatal("legacy counters were not reconstructed safely")
	}
	var html bytes.Buffer
	if err := renderReportHTML(&html, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="concurrency"`, `id="stability"`, "平均 Recall@10", "无成功 SQL 样本", "3 / 3", "最多变化 ID", "load&lt;script&gt;", "Remote&lt;script&gt;", "物理计划"} {
		if !strings.Contains(html.String(), want) {
			t.Fatalf("report missing %q", want)
		}
	}
	if strings.Contains(html.String(), "<script>") {
		t.Fatal("new report sections did not escape data")
	}
	// Hybrid QPS counts one fused request, rather than its two internal SQLs.
	for i := 0; i < 3; i++ {
		report.Scenarios[i].Route = "hybrid_rrf"
	}
	html.Reset()
	if err := renderReportHTML(&html, report); err != nil || !strings.Contains(html.String(), "成功融合请求吞吐 / QPS") {
		t.Fatalf("hybrid throughput used SQL-execution units: %v", err)
	}
	// A failed middle level is a gap, not a measured zero or a connected line.
	copy := append([]ScenarioReport(nil), view.Scenarios[:3]...)
	copy[1].SQLSuccesses = 0
	copy[2].SQLSuccesses = 1
	groups, err := buildConcurrencySeries(copy)
	if err != nil || strings.Contains(groups[0].QPSPath, "L") || strings.Count(groups[0].QPSPath, "M") != 2 {
		t.Fatalf("missing samples were plotted: %v %v", groups, err)
	}
	copy[2].EffectiveConcurrency = 1
	if _, err := buildConcurrencySeries(copy); err == nil {
		t.Fatal("duplicate profiles were merged")
	}
}

func TestRenderSelectedConcurrencyPreservesRawStabilityAndAtomicFailure(t *testing.T) {
	report := Report{Dataset: "selection", Status: "failed", Profile: Profile{ConcurrencyLevels: []int{1, 4, 8, 16}}, Errors: []string{"scenario load (client concurrency 16): 1 failed executions"}}
	for _, level := range report.Profile.ConcurrencyLevels {
		s := ScenarioReport{ID: "load", Oracle: "ann_recall", TopK: 1, EffectiveConcurrency: level, Repetitions: 1, QPS: float64(level),
			Results: []QueryResult{{ID: "q", SQLSucceeded: true, Pass: true, Score: 1, LatencyMS: float64(level * 10)}}}
		if level == 16 {
			s.Results[0].Pass, s.Results[0].Score, s.Results[0].LatencyMS, s.Failures = false, 0, 10000, 1
		}
		report.Scenarios = append(report.Scenarios, s)
	}
	stable := ScenarioReport{ID: "stable", Oracle: "stable_multiset", TopK: 1, EffectiveConcurrency: 1, Repetitions: 3, Results: []QueryResult{
		{ID: "q", SQLSucceeded: true, Pass: true, IDs: []string{"a"}, Iteration: 0, LatencyMS: 1},
		{ID: "q", SQLSucceeded: true, Pass: true, IDs: []string{"a"}, Iteration: 1, LatencyMS: 1},
		{ID: "q", SQLSucceeded: true, Pass: true, IDs: []string{"a"}, Iteration: 2, LatencyMS: 1},
	}}
	evaluateStability(&stable, []query{{ID: "q"}}, 3, 1)
	report.Scenarios = append(report.Scenarios, stable)
	view, err := makeReportView(report, 1, 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	if view.HiddenProfiles != 1 || len(view.Scenarios) != 4 || len(view.LatencyRows) != 4 || len(view.StabilityCharts) != 1 || view.ConcurrencySeries[0].QPSMaximum != 8 || view.ConcurrencySeries[0].LatencyMaximum != 80 || view.LatencyTicks[4].Value != 80 || view.Status != "failed" {
		t.Fatalf("hidden profiles affected selected metrics, stability or original verdict: %+v", view)
	}
	if len(report.Scenarios) != 5 || report.Profile.ConcurrencyLevels[3] != 16 || report.Scenarios[0].SQLSuccesses != 0 {
		t.Fatal("selection changed original profiles or counters")
	}
	// Stability remains independent even when its concurrency is not selected.
	if onlyFour, err := makeReportView(report, 4); err != nil || len(onlyFour.StabilityCharts) != 1 || len(onlyFour.Scenarios) != 2 {
		t.Fatalf("ordinary selection removed independent stability: %v", err)
	}
	// Import probes can dominate retrieval axes. They remain in the raw report
	// and are excluded only when the caller explicitly selects retrieval IDs.
	report.Scenarios = append(report.Scenarios, ScenarioReport{ID: "import_check", Oracle: "exact_ids", TopK: 1,
		EffectiveConcurrency: 1, Repetitions: 1, QPS: 1000,
		Results: []QueryResult{{ID: "doc", SQLSucceeded: true, Pass: true, Score: 1, LatencyMS: 1000}}})
	dir := t.TempDir()
	if err := writeReport(dir, report); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := renderSavedReport(dir, 1, 4, 8); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`data-level="16"`, "client concurrency 16", "10000.00 ms"} {
		if strings.Contains(string(html), forbidden) {
			t.Fatalf("unselected profile remains displayed: %s", forbidden)
		}
	}
	if !strings.Contains(string(html), "本页仅展示客户端并发 1 / 4 / 8") || !strings.Contains(string(html), `id="stability"`) {
		t.Fatal("selected scope or independent stability was omitted")
	}
	for _, invalid := range [][]int{{2}, {1, 1}, {0}, {129}, {1, 2, 3, 4, 5, 6, 7, 8, 9}} {
		if err := renderSavedReport(dir, invalid...); err == nil {
			t.Fatalf("invalid or missing selector accepted: %v", invalid)
		}
		after, err := os.ReadFile(filepath.Join(dir, "report.html"))
		if err != nil || !bytes.Equal(html, after) {
			t.Fatalf("failed selector changed prior HTML: %v", err)
		}
	}
	selection := reportSelection{Levels: []int{1, 4, 8}, ScenarioIDs: []string{"load"}}
	selectedView, err := makeSelectedReportView(report, selection)
	if err != nil || len(selectedView.Scenarios) != 4 || selectedView.HiddenProfiles != 2 || len(selectedView.StabilityCharts) != 1 || selectedView.LatencyTicks[4].Value != 80 || selectedView.ConcurrencySeries[0].QPSMaximum != 8 || selectedView.Status != "failed" {
		t.Fatalf("scenario selection lost stability, scope or metric isolation: %v %+v", err, selectedView)
	}
	if err := renderSelectedSavedReport(dir, selection); err != nil {
		t.Fatal(err)
	}
	html, err = os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil || strings.Contains(string(html), "import_check") || !strings.Contains(string(html), "本页仅展示选定场景 load") || !strings.Contains(string(html), `id="stability"`) {
		t.Fatalf("selected scenario scope was not accurately displayed: %v", err)
	}
	for _, invalid := range []reportSelection{
		{ScenarioIDs: []string{"missing"}}, {ScenarioIDs: []string{"load", "load"}},
		{ScenarioIDs: []string{""}}, {ScenarioIDs: []string{"<script>"}},
		{ScenarioIDs: []string{"stable"}}, {Levels: []int{4}, ScenarioIDs: []string{"import_check"}},
	} {
		if err := renderSelectedSavedReport(dir, invalid); err == nil {
			t.Fatalf("invalid scenario selection accepted: %+v", invalid)
		}
		after, err := os.ReadFile(filepath.Join(dir, "report.html"))
		if err != nil || !bytes.Equal(html, after) {
			t.Fatalf("failed scenario selector changed prior HTML: %v", err)
		}
	}
	if err := renderSavedReport(dir); err != nil {
		t.Fatal(err)
	}
	full, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil || !strings.Contains(string(full), `data-level="16"`) {
		t.Fatalf("unfiltered compatibility lost: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatalf("projection rewrote original measurements: %v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".report-*.html"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("failed selection leaked temporary files: %v %v", leftovers, err)
	}
}
