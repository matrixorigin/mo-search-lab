// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func saveHistoryBatch(t *testing.T, root, name string, start time.Time) []string {
	t.Helper()
	dir := filepath.Join(root, name)
	var requests []terminalRequest
	var outcomes []terminalTaskOutcome
	var paths []string
	for i, id := range []string{"gist1m_full", "gist1m_filtered_v7", "t2ranking_anli_2303643_ngram_v3", "gist_t2_sql_workload_v7"} {
		r := terminalFixture(id)
		r.Status, r.MeasurementMode = "passed", "observe"
		r.StartedAt = start.Add(time.Duration(i) * time.Minute)
		r.FinishedAt = r.StartedAt.Add(time.Minute)
		path := saveTerminalFixture(t, dir, fmt.Sprintf("%02d-%s", i+1, id), r)
		paths = append(paths, path)
		for _, name := range []string{"report.html", "environment.json"} {
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), []byte("saved evidence"), 0o640); err != nil {
				t.Fatal(err)
			}
		}
		requests = append(requests, terminalRequest{Label: r.DisplayName(), Options: options{reportDir: filepath.Dir(path)}})
		outcomes = append(outcomes, terminalTaskOutcome{Name: r.DisplayName(), Dir: filepath.Dir(path), ReportStatus: "passed"})
	}
	if err := writeTerminalBatchSummary(requests, outcomes, start); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	p := filepath.Join(dir, "batch.json")
	if err := readTerminalJSON(p, 256<<10, &doc); err != nil {
		t.Fatal(err)
	}
	doc["finished_at"] = start.Add(4 * time.Minute).Format(time.RFC3339Nano)
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(p, body, 0o640); err != nil {
		t.Fatal(err)
	}
	return paths
}

func historyFixture(t *testing.T) (*terminalModel, []string, []string) {
	t.Helper()
	root := t.TempDir()
	start := time.Date(2026, 10, 9, 9, 45, 0, 0, time.UTC)
	latest := saveHistoryBatch(t, root, "new-run", start)
	older := saveHistoryBatch(t, root, "old-run", start.Add(-3*time.Hour))
	datasets, _, err := discoverTerminalReports(root, "")
	if err != nil {
		t.Fatal(err)
	}
	m := &terminalModel{datasets: datasets, width: 100, height: 32, percentile: 95}
	choice := m.historyGroups()[0].Entries[0]
	m.dataset, m.run = choice.Dataset, choice.Run
	m.load()
	return m, latest, older
}

func TestHistoryGroupsSeparateInvocationsAndReportPageControls(t *testing.T) {
	m, latest, older := historyFixture(t)
	if len(m.historyGroups()) != 2 || len(m.historyGroups()[0].Entries) != 4 || m.doc.View.Dataset != "gist1m_full" {
		t.Fatal("history is grouped by dataset instead of invocation")
	}
	before := make(map[string]string)
	for _, p := range append(latest, older...) {
		body, _ := os.ReadFile(p)
		before[p] = string(body)
	}
	terminalKey(m, "4")
	terminalKey(m, "right")
	if m.section != 3 || m.doc.View.Dataset != "gist1m_filtered_v7" || m.datasets[m.dataset].Runs[m.run].Path != latest[1] {
		t.Fatal("arrow changed page or mixed reports from another invocation")
	}
	terminalKey(m, "left")
	terminalKey(m, "left")
	if m.section != 3 || m.datasets[m.dataset].Runs[m.run].Path != latest[3] {
		t.Fatal("report wrap escaped the selected run")
	}
	current := m.doc
	terminalKey(m, "tab")
	terminalKey(m, "2")
	if m.doc != current || m.section != 1 {
		t.Fatal("page controls changed the report")
	}
	terminalKey(m, "d")
	if !m.chooser || m.choice != 0 || !strings.Contains(m.View(), "4 份报告") {
		t.Fatal("history picker flattened the report list")
	}
	terminalKey(m, "down")
	terminalKey(m, "enter")
	if m.chooser || m.section != 1 || m.datasets[m.dataset].Runs[m.run].Path != older[3] {
		t.Fatal("choosing a run lost the selected dataset/page or opened another run")
	}
	for _, p := range append(latest, older...) {
		body, _ := os.ReadFile(p)
		if string(body) != before[p] {
			t.Fatal("navigation rewrote a raw report")
		}
	}
}

func TestDeletionEntryExistsOnlyInHistorySelection(t *testing.T) {
	m, latest, older := historyFixture(t)
	current := m.doc
	for _, width := range []int{40, 70, 105, 120} {
		m.width = width
		for page := range terminalSections {
			m.section = page
			if strings.Contains(ansi.Strip(m.View()), "删整次") || strings.Contains(ansi.Strip(m.View()), "删除整次") {
				t.Fatal("report page advertises deletion", width, page)
			}
			for _, key := range []string{"x", "delete"} {
				terminalKey(m, key)
				if m.deletion != nil || m.doc != current {
					t.Fatal("report page accepted a deletion key", width, page, key)
				}
			}
		}
	}
	m.batch = &terminalBatchSummary{outcomes: []terminalTaskOutcome{{Name: "向量检查", Dir: filepath.Dir(latest[0]), ReportStatus: "passed"}}}
	m.batchView = true
	if strings.Contains(ansi.Strip(m.View()), "删整次") {
		t.Fatal("batch results advertise deletion")
	}
	for _, key := range []string{"x", "delete"} {
		terminalKey(m, key)
		if m.deletion != nil || !m.batchView || m.batch.outcomes[0].ReportDeleted {
			t.Fatal("batch results accepted a deletion key", key)
		}
	}
	for _, path := range append(latest, older...) {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("deletion keys outside history removed a report", path, err)
		}
	}
	terminalKey(m, "d")
	if !m.chooser || m.batchView || !strings.Contains(ansi.Strip(m.View()), "x 删除整次") {
		t.Fatal("history selection lost the deletion entry")
	}
	terminalKey(m, "delete")
	if m.deletion == nil || m.deletion.Error != "" || len(m.deletion.Members) != 4 {
		t.Fatal("history selection cannot preview deletion")
	}
	terminalKey(m, "esc")
	if m.deletion != nil || !m.chooser {
		t.Fatal("cancel did not return to history selection")
	}
}

func TestWholeRunDeletionConfirmsScopeAndPreservesOtherRuns(t *testing.T) {
	m, latest, older := historyFixture(t)
	current := m.doc
	terminalKey(m, "d")
	terminalKey(m, "down")
	terminalKey(m, "x")
	if m.deletion == nil || m.deletion.Error != "" || len(m.deletion.Members) != 4 || m.deletion.Files != 13 {
		t.Fatalf("whole-run deletion scope was not prepared: %+v", m.deletion)
	}
	for _, size := range [][2]int{{40, 12}, {100, 32}} {
		m.width, m.height = size[0], size[1]
		view := ansi.Strip(m.View())
		for _, want := range []string{"删除整次运行", "4 份报告", "y 确认删除", "取消", "无法恢复"} {
			if !strings.Contains(view, want) {
				t.Fatal("confirmation omitted scope or action", size, want, view)
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if runewidth.StringWidth(line) > size[0] {
				t.Fatal("confirmation overflowed")
			}
		}
	}
	terminalKey(m, "enter")
	for _, path := range older {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("default Enter deleted a report", err)
		}
	}
	terminalKey(m, "x")
	terminalKey(m, "y")
	if m.deletion != nil || len(m.historyGroups()) != 1 || m.doc != current {
		t.Fatal("deleting the older run changed the current report or left stale groups")
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(older[0]))); !os.IsNotExist(err) {
		t.Fatal("whole run directory or batch.json survived")
	}
	for _, path := range latest {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("deleting one run damaged another", err)
		}
	}
	terminalKey(m, "x")
	terminalKey(m, "y")
	if len(m.datasets) != 0 || m.doc != nil || m.chooser || m.loadError != nil {
		t.Fatal("deleting the last whole run left a stale selection")
	}
}

func TestWholeRunDeletionPreflightsEveryReportBeforeRemovingAny(t *testing.T) {
	for _, changed := range []string{"late-data-file", "changed-batch", "unknown-root-file", "replaced-directory"} {
		t.Run(changed, func(t *testing.T) {
			m, latest, older := historyFixture(t)
			terminalKey(m, "d")
			terminalKey(m, "x")
			if m.deletion.Error != "" {
				t.Fatal(m.deletion.Error)
			}
			root := filepath.Dir(filepath.Dir(latest[0]))
			switch changed {
			case "late-data-file":
				if err := os.WriteFile(filepath.Join(filepath.Dir(latest[3]), "customer.csv"), []byte("keep"), 0o640); err != nil {
					t.Fatal(err)
				}
			case "changed-batch":
				if err := os.WriteFile(filepath.Join(root, "batch.json"), []byte("{}"), 0o640); err != nil {
					t.Fatal(err)
				}
			case "unknown-root-file":
				if err := os.WriteFile(filepath.Join(root, "customer-data.txt"), []byte("keep"), 0o640); err != nil {
					t.Fatal(err)
				}
			case "replaced-directory":
				if err := os.Rename(root, root+"-moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Dir(filepath.Dir(older[0])), root); err != nil {
					t.Fatal(err)
				}
				for i := range latest {
					latest[i] = strings.Replace(latest[i], root, root+"-moved", 1)
				}
			}
			terminalKey(m, "y")
			if m.deletion == nil || m.deletion.Error == "" {
				t.Fatal("changed deletion scope was accepted")
			}
			for _, path := range append(latest, older...) {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("preflight error deleted an earlier report or followed another run", path, err)
				}
			}
		})
	}
}

func TestOpeningBatchOrItsLeafDiscoversOnlyThatInvocation(t *testing.T) {
	_, latest, _ := historyFixture(t)
	for _, dir := range []string{filepath.Dir(latest[1]), filepath.Dir(filepath.Dir(latest[0]))} {
		datasets, _, err := discoverTerminalReports("", dir)
		m := &terminalModel{datasets: datasets}
		if err != nil || len(m.historyGroups()) != 1 || len(m.historyGroups()[0].Entries) != 4 {
			t.Fatal("opening one invocation included other runs or lost sibling reports", err)
		}
	}
}
