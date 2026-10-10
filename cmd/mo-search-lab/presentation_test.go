// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestReadableReportsPreserveHistoricalIdentityAndResults(t *testing.T) {
	r := terminalFixture("gist1m_filtered_v7")
	r.Scenarios = []ScenarioReport{{
		ID: "vector_pre_10_stability", Oracle: "stable_multiset", TopK: 100,
		EffectiveConcurrency: 1, SelectedQueries: 1, Repetitions: 4, CheckOrder: true,
		Results: []QueryResult{
			{ID: "q", SQLSucceeded: true, Pass: true, IDs: []string{"a", "a", "b"}},
			{ID: "q", SQLSucceeded: true, Error: "ID ordering changed from first execution", IDs: []string{"b", "a", "a"}},
			{ID: "q", SQLSucceeded: true, Error: "IDs changed", IDs: []string{"a", "b", "b"}},
			{ID: "q", Error: "SQL timeout"},
		},
	}}
	path := saveTerminalFixture(t, t.TempDir(), "old-report", r)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	header, err := readTerminalHeader(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loadTerminalDocument(header)
	if err != nil {
		t.Fatal(err)
	}
	if header.Dataset != r.Dataset || doc.View.Dataset != r.Dataset || doc.View.Scenarios[0].ID != r.Scenarios[0].ID || doc.View.Scenarios[0].AssertionFailures != 2 || doc.View.Scenarios[0].SQLFailures != 1 || doc.View.Status != r.Status {
		t.Fatal("presentation changed measured identity, errors or historical status")
	}
	m := &terminalModel{datasets: []terminalDataset{{ID: r.Dataset, Runs: []terminalRun{header}}}, doc: doc, width: 100, height: 32, section: 3}
	var html bytes.Buffer
	if err := renderReportHTML(&html, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"先过滤 · 可见 10% · 重复稳定性", "集合变化 1 次", "仅顺序变化 1 次", "SQL 错误 1 次", "旧版启用了严格顺序断言"} {
		if !strings.Contains(html.String(), want) || !strings.Contains(strings.Join(m.stabilityLines(), "\n"), want) {
			t.Fatalf("web/terminal omitted the measured reason: %s", want)
		}
	}
	if !strings.Contains(strings.Join(m.headerLines(), "\n"), "GIST1M · 向量过滤检查") || !strings.Contains(html.String(), "原始标识：<code>gist1m_filtered_v7</code>") {
		t.Fatal("readable title or traceable raw identity missing")
	}
	m.openReportChooser()
	if !strings.Contains(m.View(), "GIST1M · 向量过滤检查") {
		t.Fatal("history picker lost readable dataset name")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("display rewrote historical observations", err)
	}
}

func TestCustomerDisplayNameSurvivesWithoutPackAndIsEscaped(t *testing.T) {
	r := terminalFixture("customer_case")
	r.DatasetName = "客户召回复现<script>"
	path := saveTerminalFixture(t, t.TempDir(), "customer-report", r)
	header, err := readTerminalHeader(path)
	if err != nil || header.DisplayName() != r.DatasetName || header.Dataset != "customer_case" {
		t.Fatalf("custom name or executable identity lost: %+v %v", header, err)
	}
	doc, err := loadTerminalDocument(header)
	if err != nil || doc.View.DisplayName() != r.DatasetName {
		t.Fatal("reading the report required the original pack", err)
	}
	var html bytes.Buffer
	if err := renderReportHTML(&html, r); err != nil || !strings.Contains(html.String(), "客户召回复现&lt;script&gt;") || strings.Contains(html.String(), "客户召回复现<script>") {
		t.Fatal("custom label was not escaped", err)
	}
	for _, id := range []string{"customer_pre_10", "vector_pre_10_customer_case", "customer_stability", "customer_mixed"} {
		if scenarioDisplayName(id) != id || datasetDisplayName(id, "") != id {
			t.Fatal("unknown customer identity was rewritten", id)
		}
	}
}

func TestStabilityExplanationUsesSuccessfulBaselineAndMultisets(t *testing.T) {
	s := ScenarioReport{QualityMode: "observe", CheckOrder: true, Results: []QueryResult{
		{ID: "q", Error: "first SQL failed"},
		{ID: "q", SQLSucceeded: true, IDs: []string{"a", "a", "b"}},
		{ID: "empty", SQLSucceeded: true},
		{ID: "q", SQLSucceeded: true, IDs: []string{"b", "a", "a"}},
		{ID: "empty", SQLSucceeded: true, IDs: []string{}},
		{ID: "q", SQLSucceeded: true, IDs: []string{"a", "b", "b"}},
	}}
	line := stabilityChangeSummary(s)
	for _, want := range []string{"集合变化 1 次", "仅顺序变化 1 次", "SQL 错误 1 次"} {
		if !strings.Contains(line, want) {
			t.Fatal("explanation conflated execution failure, duplicate counts or ordering", line)
		}
	}
	if strings.Contains(line, "断言") {
		t.Fatal("observations acquired a historical acceptance requirement")
	}
	s.Results = []QueryResult{{ID: "q", SQLSucceeded: true}}
	if !strings.Contains(stabilityChangeSummary(s), "暂无重复比较") {
		t.Fatal("one successful sample was presented as repeated stability")
	}
	s.Results = []QueryResult{{ID: "q", Error: "SQL failed"}}
	if !strings.Contains(stabilityChangeSummary(s), "暂无成功结果可比较") {
		t.Fatal("no successful samples was presented as stable")
	}
}

func TestReadableRoutineNamesFitNarrowReportHeaders(t *testing.T) {
	for _, id := range []string{"gist1m_full", "gist1m_filtered_v7", "t2ranking_anli_2303643_ngram_v3", "gist_t2_sql_workload_v7"} {
		r := terminalFixture(id)
		m := &terminalModel{datasets: []terminalDataset{{ID: id, Runs: []terminalRun{{Dataset: id, StartedAt: r.StartedAt, Status: r.Status}}}}, width: 40, height: 12}
		title := m.reportHeaderRows()[1].Text
		if !strings.Contains(title, r.DisplayName()) || runewidth.StringWidth(title) > m.width {
			t.Fatal("dataset purpose was clipped by redundant report caption", title)
		}
	}
}
