// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"bytes"
	"flag"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLegacyFilterConversionPreservesSourcesAndTruth(t *testing.T) {
	pre := loadedScenario{scenario: scenario{ID: "vector_pre_10", Route: "sql", Oracle: "ann_recall", TopK: 100,
		SQL:     "SELECT id FROM vectors WHERE visibility_rank < ? ORDER BY l2_distance(embedding, ?) LIMIT 100 by rank with option 'mode=pre'",
		Queries: fileRef{Path: "pre.jsonl", SHA256: "query-hash"}}, Digest: "pre-hash", QueryDigest: "query-hash",
		QueriesData: []query{{ID: "q", ExactIDs: []string{"1"}, AllowedIDRanges: [][2]int64{{0, 9}}}}}
	post := pre
	post.ID, post.Digest, post.Queries.Path = "vector_post_10", "post-hash", "post.jsonl"
	post.SQL = strings.Replace(pre.SQL, "mode=pre", "mode=post", 1)
	sources := []loadedScenario{pre, post}
	unchanged, err := compatibleScenarios(sources, options{})
	if err != nil || !reflect.DeepEqual(unchanged, sources) {
		t.Fatal("default mode changed frozen scenarios", err)
	}
	converted, err := compatibleScenarios(sources, options{compat304: true})
	if err != nil || len(converted) != 1 {
		t.Fatal("equivalent legacy checks were not merged", err)
	}
	s := converted[0]
	if s.ID != "vector_default_10" || strings.Contains(s.SQL, "by rank") || !reflect.DeepEqual(s.QueriesData, pre.QueriesData) || s.Digest != "pre-hash" || s.QueryDigest != "query-hash" {
		t.Fatal("legacy conversion changed query truth or lost source identity")
	}
	if len(s.CompatibilitySources) != 2 || s.CompatibilitySources[0].SQL != pre.SQL || s.CompatibilitySources[1].SHA256 != "post-hash" || sources[0].ID != pre.ID || sources[1].SQL != post.SQL {
		t.Fatal("original SQL/identity was lost or the pack was mutated")
	}
	post.TopK = 10
	if _, err := compatibleScenarios([]loadedScenario{pre, post}, options{compat304: true}); err == nil {
		t.Fatal("different checks were silently merged")
	}
	name := scenarioDisplayName("vector_default_10_stability")
	if !strings.Contains(name, "旧版默认过滤") || !strings.Contains(name, "稳定性") {
		t.Fatal("legacy measurements mislabeled as pre/post", name)
	}
}

func TestCompatibilityFlagUIConnectionsAndReport(t *testing.T) {
	m := launchTestModel(t)
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	registerConnectionFlags(fs, &m.launchSeed)
	if err := fs.Parse([]string{"--compat-3-0-4"}); err != nil {
		t.Fatal(err)
	}
	m.openLauncher()
	f := m.launcher
	if f.fields[launchCompat304].Value != "true" {
		t.Fatal("compatibility flag did not prefill UI")
	}
	for _, inspect := range []bool{false, true} {
		f.inspecting = inspect
		request, err := f.request(m.launchSeed)
		if err != nil || !request.Options.compat304 {
			t.Fatal("UI lost compatibility choice", err)
		}
		config := makeSQLConfig(request.Options)
		if config.Params["experimental_ivf_index"] != "1" || config.Params["experimental_fulltext_index"] != "1" {
			t.Fatal("connection missing legacy session switches")
		}
	}
	f.focus = launchCompat304
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyEnter})
	request, err := f.request(m.launchSeed)
	if err != nil || request.Options.compat304 || len(makeSQLConfig(request.Options).Params) != 1 {
		t.Fatal("turning compatibility off left legacy session settings", err)
	}
	r := terminalFixture("gist1m_filtered_v7")
	r.Profile.Compat304 = true
	var html bytes.Buffer
	if err := renderReportHTML(&html, r); err != nil || !strings.Contains(html.String(), "MO 3.0.4 兼容模式") {
		t.Fatal("HTML omitted compatibility identity", err)
	}
}
