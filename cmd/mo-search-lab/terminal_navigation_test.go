// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func navigationFixture(t *testing.T) (*terminalModel, []string) {
	t.Helper()
	root := t.TempDir()
	r := terminalFixture("gist")
	paths := []string{saveTerminalFixture(t, root, "latest", r)}
	r.StartedAt = r.StartedAt.Add(-time.Hour)
	paths = append(paths, saveTerminalFixture(t, root, "older", r))
	r.Dataset = "t2"
	r.StartedAt = r.StartedAt.Add(-time.Hour)
	paths = append(paths, saveTerminalFixture(t, root, "text", r))
	datasets, _, err := discoverTerminalReports(root, "")
	if err != nil {
		t.Fatal(err)
	}
	m := &terminalModel{datasets: datasets, width: 100, height: 32, percentile: 95, reportsRoot: root, run: 1}
	m.load()
	if m.loadError != nil {
		t.Fatal(m.loadError)
	}
	return m, paths
}

func TestPageKeysNeverChangeReportOrResetItsFilters(t *testing.T) {
	m, paths := navigationFixture(t)
	m.level, m.scenario = 4, 1
	doc := m.doc
	before := make(map[string]string)
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = string(body)
	}
	for _, step := range []struct {
		key  string
		page int
	}{
		{"tab", 1}, {"shift+tab", 0},
		{"6", 5}, {"tab", 0}, {"shift+tab", 5},
		{"1", 0}, {"2", 1}, {"3", 2}, {"4", 3}, {"5", 4}, {"6", 5},
		{"?", 6}, {"?", 5}, {"?", 6}, {"tab", 0},
	} {
		m.scroll = 7
		terminalKey(m, step.key)
		if m.section != step.page || m.scroll != 0 || m.dataset != 0 || m.run != 1 || m.doc != doc || m.level != 4 || m.scenario != 1 {
			t.Fatalf("%s changed the report, hidden filters or wrong page: page=%d dataset=%d run=%d level=%d scenario=%d", step.key, m.section, m.dataset, m.run, m.level, m.scenario)
		}
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != before[path] {
			t.Fatal("navigation modified raw report", filepath.Base(path), err)
		}
	}
}

func TestReportSelectionIsExplicitAndContextKeysStayLocal(t *testing.T) {
	m, _ := navigationFixture(t)
	doc := m.doc
	terminalKey(m, "4")
	m.level = 4
	if strings.Contains(m.reportFooter()[1].Text, "c 并发") || !strings.Contains(strings.Join(m.headerLines(), "\n"), "并发 1") || strings.Contains(strings.Join(m.headerLines(), "\n"), "并发筛选 4") {
		t.Fatal("stability advertises a control or filter from another page")
	}
	m.level = 0
	for _, key := range []string{"p", "c", "n", "b"} {
		terminalKey(m, key)
	}
	if m.percentile != 95 || m.level != 0 || m.scenario != 0 {
		t.Fatal("stability page changed hidden controls")
	}
	terminalKey(m, "d")
	if !m.chooser || m.choices()[m.choice] != (terminalChoice{0, 1}) {
		t.Fatal("chooser did not focus the current historical run")
	}
	choice := m.choice
	for _, key := range []string{"left", "right", "tab", "2"} {
		terminalKey(m, key)
	}
	if m.choice != choice || m.section != 3 || m.doc != doc || m.run != 1 {
		t.Fatal("page shortcuts changed the report inside selection")
	}
	terminalKey(m, "esc")
	terminalKey(m, "r") // Compatibility alias opens selection, never cycles silently.
	if !m.chooser || m.doc != doc || m.run != 1 {
		t.Fatal("historical-run alias silently changed the report")
	}
	terminalKey(m, "down")
	terminalKey(m, "enter")
	if m.chooser || m.dataset != 1 || m.run != 0 || m.section != 3 || m.doc.View.Dataset != "t2" {
		t.Fatal("explicit selection lost the report page")
	}
	terminalKey(m, "?")
	terminalKey(m, "d")
	m.choice = 0
	terminalKey(m, "enter")
	if m.section != 3 || m.dataset != 0 || m.run != 0 {
		t.Fatal("opening a report from help did not return to the report page")
	}
	terminalKey(m, "3")
	terminalKey(m, "p")
	terminalKey(m, "n")
	terminalKey(m, "c")
	if m.percentile != 95 || m.scenario != 0 || m.level != 1 {
		t.Fatal("quality controls affected another page")
	}
	if strings.Contains(strings.Join(m.headerLines(), "\n"), "P95") || strings.Contains(m.reportFooter()[1].Text, "p 分位数") {
		t.Fatal("quality page advertises a latency control")
	}
	terminalKey(m, "5")
	terminalKey(m, "c")
	terminalKey(m, "p")
	terminalKey(m, "n")
	if m.level != 1 || m.percentile != 95 || m.scenario != 1 {
		t.Fatal("SQL controls affected hidden filtering")
	}
	for _, size := range [][2]int{{40, 12}, {60, 18}, {90, 24}, {100, 32}} {
		m.width, m.height = size[0], size[1]
		body := ansi.Strip(m.View())
		if strings.Contains(body, "←/→ 数据集") || (!strings.Contains(body, "←→") || !strings.Contains(body, "Tab")) || !strings.Contains(body, "q 退出") || !strings.Contains(body, "SQL") || len(strings.Split(body, "\n")) > size[1] {
			t.Fatal("navigation hints overlap or disappear", size, body)
		}
	}
}
