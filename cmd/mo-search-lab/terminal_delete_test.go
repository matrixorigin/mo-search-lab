// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func TestReportDeletionConfirmationAndHistory(t *testing.T) {
	root := t.TempDir()
	latest := terminalFixture("gist")
	latestPath := saveTerminalFixture(t, root, "latest", latest)
	old := latest
	old.StartedAt = old.StartedAt.Add(-time.Hour)
	oldPath := saveTerminalFixture(t, root, "old", old)
	other := terminalFixture("text")
	other.StartedAt = other.StartedAt.Add(-2 * time.Hour)
	otherPath := saveTerminalFixture(t, root, "other", other)
	latestBytes, _ := os.ReadFile(latestPath)
	otherBytes, _ := os.ReadFile(otherPath)
	for _, name := range []string{"report.html", "environment.json", "stability-tie-analysis.json", "run.log"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(oldPath), name), []byte("saved evidence"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	datasets, _, err := discoverTerminalReports(root, "")
	if err != nil {
		t.Fatal(err)
	}
	m := &terminalModel{datasets: datasets, percentile: 95, width: 100, height: 32}
	m.load()
	current := m.doc
	terminalKey(m, "d")
	terminalKey(m, "down")
	for _, cancel := range []string{"enter", "n", "esc"} {
		terminalKey(m, "x")
		if m.deletion == nil || m.deletion.Run.Path != oldPath {
			t.Fatal("delete did not select the focused historical run")
		}
		for _, size := range [][2]int{{40, 12}, {100, 32}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "gist") || !strings.Contains(view, "y 确认删除") || !strings.Contains(view, "取消") || !strings.Contains(view, "无法恢复") {
				t.Fatal("confirmation lost report identity or actions", view)
			}
			lines := strings.Split(view, "\n")
			if len(lines) > size[1] {
				t.Fatal("confirmation exceeds viewport")
			}
			for _, line := range lines {
				if runewidth.StringWidth(line) > size[0] {
					t.Fatal("confirmation exceeds terminal width")
				}
			}
		}
		terminalKey(m, cancel)
		if m.deletion != nil || !m.chooser {
			t.Fatal("cancellation did not restore history selection")
		}
		if _, err := os.Stat(oldPath); err != nil {
			t.Fatal("cancel deleted a file", err)
		}
	}
	terminalKey(m, "x")
	terminalKey(m, "y")
	if _, err := os.Stat(filepath.Dir(oldPath)); !os.IsNotExist(err) {
		t.Fatal("report directory and sidecars survived confirmation", err)
	}
	if m.deletion != nil || !m.chooser || len(m.datasets) != 2 || len(m.datasets[0].Runs) != 1 || m.doc != current {
		t.Fatal("deleting a historical run changed the current report or left stale history")
	}
	for path, expected := range map[string]string{latestPath: string(latestBytes), otherPath: string(otherBytes)} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != expected {
			t.Fatal("an unselected report changed", path, err)
		}
	}
	// The next historical entry is focused after removal; delete its dataset.
	terminalKey(m, "x")
	terminalKey(m, "y")
	if len(m.datasets) != 1 || m.datasets[0].ID != "gist" || m.doc != current {
		t.Fatal("empty dataset was not removed, or remaining current report changed")
	}
	terminalKey(m, "esc")
	terminalKey(m, "d")
	terminalKey(m, "x")
	terminalKey(m, "y")
	if len(m.datasets) != 0 || m.doc != nil || m.chooser || m.loadError != nil {
		t.Fatal("last deletion retained stale indices or document")
	}
	if !strings.Contains(m.View(), "暂无报告") || !strings.Contains(m.View(), "q 退出") {
		t.Fatal("empty state is not usable", m.View())
	}
	remaining, _, err := discoverTerminalReports(root, "")
	if err != errNoTerminalReports || len(remaining) != 0 {
		t.Fatal("deleted runs returned after rediscovery", remaining, err)
	}
}

func TestReportDeletionChangedTargetsAndMixedDirectories(t *testing.T) {
	for _, kind := range []string{"report changed", "directory replaced", "nested reports", "data pack", "csv input", "project", "batch summary"} {
		t.Run(kind, func(t *testing.T) {
			m, path := newTerminalFixtureModel(t)
			terminalKey(m, "d")
			terminalKey(m, "x")
			if m.deletion == nil || m.deletion.Error != "" {
				t.Fatal("valid report could not open confirmation")
			}
			dir := filepath.Dir(path)
			switch kind {
			case "report changed":
				body, _ := os.ReadFile(path)
				if err := os.WriteFile(path, append(body, '\n'), 0o640); err != nil {
					t.Fatal(err)
				}
			case "directory replaced":
				if err := os.Rename(dir, dir+"-original"); err != nil {
					t.Fatal(err)
				}
				saveTerminalFixture(t, filepath.Dir(dir), filepath.Base(dir), terminalFixture("other"))
			case "nested reports":
				saveTerminalFixture(t, dir, "nested", terminalFixture("nested"))
			default:
				name := map[string]string{"data pack": "manifest.json", "csv input": "documents.csv", "project": "go.mod", "batch summary": "batch.json"}[kind]
				if err := os.WriteFile(filepath.Join(dir, name), []byte("preserve input"), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			terminalKey(m, "y")
			if m.deletion == nil || m.deletion.Error == "" || len(m.datasets) != 1 || m.doc == nil {
				t.Fatal("unsafe/changed directory was deleted or failure was hidden")
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != string(before) {
				t.Fatal("failed validation changed report files", err)
			}
			terminalKey(m, "esc")
		})
	}
}

func TestReportDeletionLoadsRemainingCurrentRun(t *testing.T) {
	m, path := newTerminalFixtureModel(t)
	saveTerminalFixture(t, filepath.Dir(filepath.Dir(path)), "other", terminalFixture("text"))
	datasets, _, err := discoverTerminalReports(filepath.Dir(filepath.Dir(path)), "")
	if err != nil {
		t.Fatal(err)
	}
	m.datasets = datasets
	for i, dataset := range datasets {
		if dataset.ID == "gist" {
			m.dataset = i
		}
	}
	m.load()
	terminalKey(m, "d")
	terminalKey(m, "x")
	terminalKey(m, "y")
	if len(m.datasets) != 1 || m.dataset != 0 || m.run != 0 || m.doc == nil || m.doc.View.Dataset != "text" {
		t.Fatal("current deletion did not load the remaining run")
	}
	if !strings.Contains(m.View(), "text") {
		t.Fatal("remaining report is not visible")
	}
}

func TestReportDeletionDoesNotFollowSymlinks(t *testing.T) {
	m, path := newTerminalFixtureModel(t)
	out := t.TempDir()
	outside := filepath.Join(out, "preserved.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(path), "evidence-link")); err != nil {
		t.Skip(err)
	}
	terminalKey(m, "d")
	terminalKey(m, "x")
	terminalKey(m, "y")
	body, err := os.ReadFile(outside)
	if err != nil || string(body) != "keep" {
		t.Fatal("report deletion followed a file symlink", err)
	}
	// A directory link may be viewed explicitly but must not become a delete root.
	root := t.TempDir()
	path = saveTerminalFixture(t, root, "original", terminalFixture("gist"))
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Dir(path), link); err != nil {
		t.Fatal(err)
	}
	header, err := readTerminalHeader(filepath.Join(link, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTerminalReportDeletion(header); err == nil {
		t.Fatal("report deletion accepted a directory symlink")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("directory link validation changed original report", err)
	}
}

func TestReportDeletionRetiresBatchLinkAndPreservesExecutionJournal(t *testing.T) {
	m, path := newTerminalFixtureModel(t)
	dir := filepath.Dir(path)
	journal := filepath.Join(filepath.Dir(dir), "batch.json")
	before := `{"items":[{"status":"passed","report_dir":"run"}]}`
	if err := os.WriteFile(journal, []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	m.batch = &terminalBatchSummary{outcomes: []terminalTaskOutcome{{Name: "向量检查", Dir: dir, ReportStatus: "passed"}}}
	m.batchView = true
	terminalKey(m, "d")
	terminalKey(m, "x")
	terminalKey(m, "y")
	terminalKey(m, "t")
	if !m.batch.outcomes[0].ReportDeleted || m.batch.outcomes[0].ReportStatus != "passed" || !strings.Contains(m.View(), "报告已删除") {
		t.Fatal("batch result retained an active link or changed the measured outcome")
	}
	terminalKey(m, "enter")
	terminalKey(m, "esc")
	if !m.batchView || m.doc != nil || len(m.datasets) != 0 {
		t.Fatal("opening a deleted batch report registered a stale entry")
	}
	body, err := os.ReadFile(journal)
	if err != nil || string(body) != before {
		t.Fatal("deleting a leaf changed the original execution journal", err)
	}
}
