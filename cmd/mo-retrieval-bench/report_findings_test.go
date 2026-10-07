// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestIndependentFindingsAndObservedQualityRendering(t *testing.T) {
	r := Report{Dataset: "fixture", Status: "failed", Cleanup: "dropped", Scenarios: []ScenarioReport{
		{ID: "anli", Oracle: "qrels", TopK: 100, NDCGGain: "linear", QualityMode: "observe", RelevantGrade: 2, EffectiveConcurrency: 1,
			Results: []QueryResult{{ID: "q", SQLSucceeded: true, Pass: true, Quality: &QualityMetrics{}}}},
		{ID: "historical_quality_gate", Oracle: "qrels", Results: []QueryResult{{ID: "q", SQLSucceeded: true, Pass: false}}},
		{ID: "repeat", Oracle: "stable_multiset", CheckOrder: true, Failures: 1,
			Results: []QueryResult{{ID: "q", SQLSucceeded: true, Pass: false}}},
	}}
	checks := summarizeChecks(r)
	if checks.Health != "通过" || checks.Stability != "有断言未通过" || checks.SQLSamples != 3 || checks.QualitySamples != 2 || checks.StabilityFailures != 1 {
		t.Fatalf("quality/stability was mistaken for SQL health: %+v", checks)
	}
	var html bytes.Buffer
	if err := renderReportHTML(&html, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), `id="findings"`) {
		t.Fatal("removed summary cards were rendered")
	}
	for _, required := range []string{"观察，无验收阈值", "nDCG@10", "Recall@100", "空结果 1 次", "检查顺序"} {
		if !strings.Contains(html.String(), required) {
			t.Fatalf("missing independent evidence: %s", required)
		}
	}
	r.Scenarios[0].Results = append(r.Scenarios[0].Results, QueryResult{ID: "sql-error"})
	if got := summarizeChecks(r); got.Health != "异常" || got.SQLErrors != 1 {
		t.Fatalf("SQL error did not fail health: %+v", got)
	}
	r.Scenarios[0].Results = nil
	if got := summarizeChecks(r); got.Health != "未完成" {
		t.Fatalf("unexecuted scene appeared healthy: %+v", got)
	}
}
