// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoricalESNotesRemainIndependentOfSavedRun(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"dataset":"t2ranking_anli_2303643_ngram_v3","status":"passed","scenarios":[{"id":"anli_sql_bm25","effective_concurrency":1,"qps":2.5,"results":[{"latency_ms":400,"sql_succeeded":true,"pass":true}]}]}`)
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := renderSavedReport(dir, 1); err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(dir, "report.html"))
	if err != nil {
		t.Fatal(err)
	}
	_, tail, found := strings.Cut(string(page), `<details id="report-notes"`)
	if !found {
		t.Fatal("T2Ranking report has no historical ES notes")
	}
	notes, _, _ := strings.Cut(tail, "</details>")
	for _, want := range []string{"2026-10-06", "独立于本次 MO 运行", "1165.50", "60,000", "31.08 GiB", "8 CPU、16 GiB", "verification.json"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("historical notes missing %q", want)
		}
	}
	if strings.Contains(notes, "<script") || strings.Contains(notes, "href=") || strings.Contains(notes, " open") {
		t.Fatal("notes should be collapsed and readable offline")
	}
	performance, environment, found := strings.Cut(string(page), `<section id="report-environment"`)
	if !found || !strings.Contains(performance, `id="report-notes"`) || strings.Contains(environment, "Intel Core i7") {
		t.Fatal("historical ES hardware was mixed with current MO environment")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatalf("notes changed original run evidence: %v", err)
	}
}

func TestHistoricalESNotesOnlyAppearInT2RankingBenchmarks(t *testing.T) {
	for _, report := range []Report{
		{Dataset: "gist1m_full"},
		{Dataset: "gist_t2_sql_workload_v7"},
		{Dataset: "custom_fulltext"},
		{Dataset: "t2ranking_anli_fixture", RunKind: "environment_inspection"},
	} {
		var page bytes.Buffer
		if err := renderReportHTML(&page, report); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(page.String(), `id="report-notes"`) {
			t.Fatalf("unrelated report %s / %s contains ES notes", report.Dataset, report.RunKind)
		}
	}
}
