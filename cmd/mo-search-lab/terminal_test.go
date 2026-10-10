// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

func terminalFixture(dataset string) Report {
	r := Report{Dataset: dataset, ToolVersion: "test-measurement", Status: "failed", StartedAt: time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC), Profile: Profile{Concurrency: 1, ConcurrencyLevels: []int{1, 4, 8}}, ResourceMetrics: "unavailable"}
	for _, level := range []int{1, 4, 8} {
		r.Scenarios = append(r.Scenarios, ScenarioReport{ID: "vector", Oracle: "ann_recall", TopK: 100, EffectiveConcurrency: level, QualityMode: "observe", Repetitions: 1, Executions: 3, MeasuredSeconds: 10, QPS: 0.2, ScenarioSHA256: "fixed-scenario", QueriesSHA256: "fixed-queries", SQL: "SELECT id FROM vectors ORDER BY l2_distance(embedding, ?) LIMIT 100", Results: []QueryResult{
			{ID: "a", LatencyMS: float64(level * 10), SQLSucceeded: true, Pass: true, Score: 0.5, IDs: []string{"1", "2"}},
			{ID: "b", LatencyMS: float64(level * 20), SQLSucceeded: true, Pass: false, Score: 0.25},
			{ID: "c", LatencyMS: 99999, Error: "SQL failed"},
		}})
	}
	r.Scenarios = append(r.Scenarios,
		ScenarioReport{ID: "empty", Oracle: "ann_recall", EffectiveConcurrency: 1, P95MS: 99999, QPS: 99999, Results: []QueryResult{{ID: "empty", Error: "failure"}}},
		ScenarioReport{ID: "zero", Oracle: "ann_recall", EffectiveConcurrency: 1, Results: []QueryResult{{ID: "zero", SQLSucceeded: true, Pass: true}}},
		ScenarioReport{ID: "text", Oracle: "qrels", EffectiveConcurrency: 1, TopK: 100, NDCGGain: "linear", RelevantGrade: 2, QualityMode: "observe", Results: []QueryResult{{ID: "text", SQLSucceeded: true, Pass: true, Score: 0.4, Quality: &QualityMetrics{NDCG: 0.4, NDCG10: 0.2, Recall: 0.6, MRR10: 0.5}}}},
		ScenarioReport{ID: "stable", Oracle: "stable_multiset", EffectiveConcurrency: 1, TopK: 100, Repetitions: 4, SelectedQueries: 1, CheckOrder: true, Results: []QueryResult{
			{ID: "q", Iteration: 0, SQLSucceeded: true, Pass: true, IDs: []string{"1", "2"}},
			{ID: "q", Iteration: 1, SQLSucceeded: true, IDs: []string{"2", "1"}},
			{ID: "q", Iteration: 2, Error: "timeout"},
		}, Stability: []StabilityResult{{QueryID: "q", DistinctResults: 1, DistinctOrders: 2, WorstOverlap: 1}}},
	)
	return r
}

func saveTerminalFixture(t *testing.T, root, name string, report Report) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, body, 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTerminalFixtureModel(t *testing.T) (*terminalModel, string) {
	t.Helper()
	root := t.TempDir()
	path := saveTerminalFixture(t, root, "run", terminalFixture("gist"))
	datasets, warnings, err := discoverTerminalReports(root, "")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("discover: %v %v", err, warnings)
	}
	m := &terminalModel{datasets: datasets, percentile: 95, width: 100, height: 32}
	m.load()
	if m.loadError != nil {
		t.Fatal(m.loadError)
	}
	return m, path
}

func TestTerminalCatalogueAndBounds(t *testing.T) {
	root := t.TempDir()
	latest := terminalFixture("gist")
	saveTerminalFixture(t, root, "latest", latest)
	old := latest
	old.StartedAt = old.StartedAt.Add(-time.Hour)
	saveTerminalFixture(t, root, "old", old)
	text := latest
	text.Dataset, text.StartedAt = "t2", text.StartedAt.Add(-2*time.Hour)
	saveTerminalFixture(t, root, "other", text)
	path := saveTerminalFixture(t, root, "broken", latest)
	if err := os.WriteFile(path, []byte("{"), 0o640); err != nil {
		t.Fatal(err)
	}
	// The catalogue must not follow dataset symlinks.
	if err := os.Symlink(filepath.Join(root, "latest"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	datasets, warnings, err := discoverTerminalReports(root, "")
	if err != nil || len(warnings) != 1 || len(datasets) != 2 || datasets[0].ID != "gist" || len(datasets[0].Runs) != 2 || datasets[0].Runs[0].StartedAt != latest.StartedAt {
		t.Fatalf("catalogue/order: %+v %v %v", datasets, warnings, err)
	}
	if _, _, err := discoverTerminalReports(t.TempDir(), ""); err == nil {
		t.Fatal("empty catalogue accepted")
	}
	if _, _, err := discoverTerminalReports("", filepath.Dir(path)); err == nil {
		t.Fatal("malformed single report accepted")
	}
	if _, err := readTerminalHeader(filepath.Join(root, "absent")); err == nil {
		t.Fatal("missing file accepted")
	}
	f, err := os.Create(filepath.Join(root, "oversized.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(terminalReportLimit + 1); err != nil {
		t.Fatal(err)
	}
	if _, err := readTerminalHeader(f.Name()); err == nil {
		t.Fatal("oversized report accepted")
	}
}

func TestTerminalMeasurementsAndEscaping(t *testing.T) {
	m, path := newTerminalFixtureModel(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for page, wanted := range map[int][]string{
		0: {"P95 延迟", "20.000", "暂无成功样本", "0.000"},
		1: {"向量检索 / C4", "80.000", "向量检索 / C8", "160.000", "成功查询吞吐"},
		2: {"Recall@100", "0.375", "平均返回 1.00", "nDCG@10", "0.200", "0.400", "0.600", "0.500"},
		3: {"●!×·", "集合 1 种，顺序 2 种", "失败 2/3"},
		4: {"SELECT id FROM vectors", "LIMIT 100", "fixed-queries"},
		5: {"版本与部署", "节点与缓存", "资源监控 · 未采集", "未提供", "--environment-details"},
		6: {"MRR@10", "排名倒数", "最多 2C 条 SQL"},
	} {
		m.section = page
		var body bytes.Buffer
		if err := m.writePlain(&body); err != nil {
			t.Fatal(err)
		}
		for _, want := range wanted {
			if !strings.Contains(body.String(), want) {
				t.Fatalf("page %d missing %q:\n%s", page, want, body.String())
			}
		}
		if strings.Contains(body.String(), "99999.000") || strings.Contains(body.String(), "\x1b") {
			t.Fatalf("page %d included failed latency/stale metric/control", page)
		}
		if !strings.Contains(body.String(), "原始整体判定: 存在未通过检查") {
			t.Fatal("raw failure status hidden")
		}
	}
	m.section, m.environmentDetails = 5, true
	var details bytes.Buffer
	if err := m.writePlain(&details); err != nil || !strings.Contains(details.String(), "test-measurement") || !strings.Contains(details.String(), "运行身份与输入记录") {
		t.Fatal("expanded environment lost measurement identity")
	}
	for _, width := range []int{40, 60, 100} {
		m.width = width
		for page := range terminalSections {
			m.section = page
			for _, line := range m.contentLines() {
				if runewidth.StringWidth(line) > width {
					t.Fatalf("page %d exceeded %d columns: %s", page, width, line)
				}
			}
		}
	}
	for _, line := range terminalWrap("中文\x1b[31m\x07\r界面\tSQL\n\u202eSAFE", 8) {
		if strings.ContainsAny(line, "\x1b\x07\r\t\u202e") || runewidth.StringWidth(line) > 8 {
			t.Fatalf("unsafe/oversized line: %q", line)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("viewer changed raw measurements: %v", err)
	}
}

func terminalKey(m *terminalModel, key string) tea.Cmd {
	var msg tea.KeyMsg
	switch key {
	case "left":
		msg.Type = tea.KeyLeft
	case "right":
		msg.Type = tea.KeyRight
	case "tab":
		msg.Type = tea.KeyTab
	case "shift+tab":
		msg.Type = tea.KeyShiftTab
	case "esc":
		msg.Type = tea.KeyEsc
	case "down":
		msg.Type = tea.KeyDown
	case "enter":
		msg.Type = tea.KeyEnter
	case "end":
		msg.Type = tea.KeyEnd
	default:
		msg.Type, msg.Runes = tea.KeyRunes, []rune(key)
	}
	_, cmd := m.Update(msg)
	return cmd
}

func TestTerminalNavigationAndFailedSwitch(t *testing.T) {
	m, path := newTerminalFixtureModel(t)
	for _, key := range []string{"2", "p", "c", "c"} {
		terminalKey(m, key)
	}
	if m.section != 1 || m.percentile != 99 || m.level != 4 {
		t.Fatalf("navigation: %+v", m)
	}
	body := strings.Join(m.contentLines(), "\n")
	if !strings.Contains(body, "向量检索 / C4") || strings.Contains(body, "向量检索 / C8") {
		t.Fatal("profile selector did not isolate the measured level")
	}
	terminalKey(m, "4")
	if !strings.Contains(strings.Join(m.contentLines(), "\n"), "●!×·") {
		t.Fatal("concurrency selection hid independent stability")
	}
	terminalKey(m, "?")
	terminalKey(m, "?")
	if m.section != 3 {
		t.Fatal("help did not restore prior page")
	}
	terminalKey(m, "5")
	terminalKey(m, "n")
	if m.selectedScenario().EffectiveConcurrency != 4 {
		t.Fatal("SQL scenario navigation did not change profile")
	}
	terminalKey(m, "b")
	if m.selectedScenario().EffectiveConcurrency != 1 {
		t.Fatal("previous scenario failed")
	}
	terminalKey(m, "end")
	view := m.View()
	if len(strings.Split(view, "\n")) > m.height {
		t.Fatal("viewport exceeds terminal height")
	}
	for _, size := range [][2]int{{40, 12}, {100, 32}, {8, 3}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if len(strings.Split(m.View(), "\n")) > size[1] {
			t.Fatalf("resize overflow: %v", size)
		}
	}
	// Discover a new report, then corrupt its body after catalogue publication.
	r := terminalFixture("broken-dataset")
	r.StartedAt = r.StartedAt.Add(-time.Hour)
	broken := saveTerminalFixture(t, filepath.Dir(filepath.Dir(path)), "broken", r)
	datasets, _, err := discoverTerminalReports(filepath.Dir(filepath.Dir(path)), "")
	if err != nil {
		t.Fatal(err)
	}
	m.datasets = datasets
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	if err := os.WriteFile(broken, []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	terminalKey(m, "d")
	terminalKey(m, "down")
	terminalKey(m, "enter")
	if m.doc != nil || m.loadError == nil || m.level != 0 || m.scenario != 0 {
		t.Fatal("failed switch retained stale document/profile")
	}
	if !strings.Contains(strings.Join(m.contentLines(), "\n"), "读取失败") {
		t.Fatal("failed switch was invisible")
	}
	terminalKey(m, "d")
	terminalKey(m, "down")
	// The chooser starts at the broken entry, so select the first entry explicitly.
	m.choice = 0
	terminalKey(m, "enter")
	if m.chooser || m.loadError != nil || m.doc.View.Dataset != "gist" {
		t.Fatal("chooser could not recover from failed loading")
	}
	cmd := terminalKey(m, "q")
	if cmd == nil {
		t.Fatal("quit produced no terminal lifecycle command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("quit did not request terminal restoration")
	}
}

func TestTerminalInputErrorsAndSidecars(t *testing.T) {
	m, path := newTerminalFixtureModel(t)
	for _, name := range []string{"environment.json", "stability-tie-analysis.json"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), []byte("{"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	m.load()
	if m.loadError != nil || !strings.Contains(m.doc.Evidence["environment.json"], "读取失败") {
		t.Fatal("bad supplementary evidence hidden or invalidated valid measurements")
	}
	for name, alter := range map[string]func(*Report){
		"no scenarios": func(r *Report) { r.Scenarios = nil },
		"bad latency":  func(r *Report) { r.Scenarios[0].Results[0].LatencyMS = -1 },
		"bad quality": func(r *Report) {
			r.Scenarios[0].Results[0].Quality = &QualityMetrics{Recall: 1.1}
		},
		"bad repetitions": func(r *Report) { r.Scenarios[0].Repetitions = 100001 },
		"bad iteration": func(r *Report) {
			r.Scenarios[0].Results[0].Iteration = 100000
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := terminalFixture(name)
			alter(&r)
			bad := saveTerminalFixture(t, t.TempDir(), "bad", r)
			header, err := readTerminalHeader(bad)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loadTerminalDocument(header); err == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
	if err := os.WriteFile(path, append([]byte(`{"dataset":"gist","status":"failed","tool_version":"test","started_at":"2026-10-07T00:00:00Z",`), []byte(`"scenarios":[INVALID`)...), 0o640); err != nil {
		t.Fatal(err)
	}
	header, err := readTerminalHeader(path)
	if err != nil {
		t.Fatal("catalogue read beyond leading metadata", err)
	}
	if _, err := loadTerminalDocument(header); err == nil {
		t.Fatal("malformed report body accepted")
	}
	for _, args := range [][]string{{"--reports", "x", "--report-dir", "y"}, {"--percentile", "50"}, {"--width", "8"}, {"--section", "unknown"}, {"--concurrency", "129"}} {
		if err := runTerminalUI(args); err == nil {
			t.Fatalf("invalid flags accepted: %v", args)
		}
	}
	if err := m.writePlain(terminalFailWriter{}); !errors.Is(err, errTerminalWrite) {
		t.Fatalf("output error swallowed: %v", err)
	}
}

var errTerminalWrite = fmt.Errorf("test output failure")

type terminalFailWriter struct{}

func (terminalFailWriter) Write([]byte) (int, error) { return 0, errTerminalWrite }
