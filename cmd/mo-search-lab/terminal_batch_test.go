// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func TestCheckedPacksConfirmCancelAndPerPackOptions(t *testing.T) {
	root := t.TempDir()
	healthScenes := []string{"vector.json", "vector_nprobe_20.json", "vector_nprobe_100.json", "vector_stability.json"}
	health := saveCataloguePack(t, root, "health", "gist1m_full", 1000000, 100, healthScenes...)
	filter := saveCataloguePack(t, root, "filter", "gist1m_filtered_v7", 1000000, 100)
	saveCataloguePack(t, root, "text", "t2ranking_anli_2303643_ngram_v3", 2303643, 100)
	paired := saveCataloguePack(t, root, "paired", "gist_t2_sql_workload_v7", 3303643, 100, "vector.json", "fulltext.json")
	m := &terminalModel{width: 100, height: 32, packsRoot: root, reportsRoot: t.TempDir(), launchSeed: defaultTerminalOptions()}
	m.openLauncher()
	f := m.launcher
	requests, err := f.requests(m.launchSeed)
	if err != nil || len(requests) != 4 {
		t.Fatalf("default is not all routine tests: %v %v", requests, err)
	}
	for i, request := range requests {
		if !request.Options.defaultConcurrency || request.Options.queryLimit != 0 || request.Options.stabilityRepeat != 30 {
			t.Fatal("shared measurement defaults changed")
		}
		if i < 3 && len(request.Options.mixedScenarios) > 0 || i == 3 && strings.Join(request.Options.mixedScenarios, ",") != "vector,fulltext" {
			t.Fatal("paired settings leaked across projects")
		}
		if i > 0 && request.Options.reportDir == requests[i-1].Options.reportDir {
			t.Fatal("selected projects share a report target")
		}
	}
	scenes := []loadedScenario{{scenario: scenario{Route: "sql", Oracle: "ann_recall"}, QueriesData: []query{{AllowedIDRanges: [][2]int64{{0, 99}}}}}}
	if got := defaultPackProfile(scenes, requests[1].Options); len(got.concurrencyLevels) != 1 || got.concurrencyLevels[0] != 1 {
		t.Fatal("filter test lost its serial default")
	}
	f.openPackChooser()
	m.updateLauncher(tea.KeyMsg{Type: tea.KeySpace})
	if f.packChecked(health) || len(f.selectedPacks) != 3 {
		t.Fatal("space did not toggle just the focused project")
	}
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyEsc})
	if len(f.selectedPacks) != 4 || !f.packChecked(health) {
		t.Fatal("Esc did not restore pre-edit selections")
	}
	f.openPackChooser()
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyEnter})
	if !f.choosing || f.err == "" {
		t.Fatal("empty selection was accepted")
	}
	if _, err := f.requests(m.launchSeed); err == nil {
		t.Fatal("empty selection launched a job")
	}
	m.updateLauncher(tea.KeyMsg{Type: tea.KeySpace})
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyEnter})
	if f.choosing || len(f.selectedPacks) != 1 || !f.packChecked(health) {
		t.Fatal("confirmed checkbox choices were discarded")
	}
	f.setSelectedPacks([]string{paired, filter})
	requests, err = f.requests(m.launchSeed)
	if err != nil || requests[0].Options.pack != filter || requests[1].Options.pack != paired {
		t.Fatal("click order changed stable execution order", err)
	}
	f.toggleConcurrency(paired, 4)
	requests, err = f.requests(m.launchSeed)
	if err != nil || len(requests[0].Options.concurrencyLevels) != 1 || len(requests[1].Options.concurrencyLevels) != 2 {
		t.Fatal("per-dataset concurrency was lost or leaked", err)
	}
	f.focus = launchPack
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !strings.Contains(m.View(), "▏") {
		t.Fatal("editing a single path lost its cursor")
	}
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyEnter})
	if len(f.selectedPacks) != 1 || !strings.HasSuffix(f.selectedPacks[0], "x") {
		t.Fatal("manual path did not replace checkbox selection")
	}
}

func TestBatchRunsSeriallyAndRetainsIndependentReports(t *testing.T) {
	m, oldPath := newTerminalFixtureModel(t)
	before, _ := os.ReadFile(oldPath)
	root := t.TempDir()
	var running atomic.Int32
	var order []string
	m.executeTask = func(ctx context.Context, request terminalRequest) terminalTaskResult {
		if running.Add(1) != 1 {
			t.Error("projects overlap and contaminate measurement load")
		}
		defer running.Add(-1)
		order = append(order, request.Label)
		notifyProgress(ctx, "执行查询")
		r := terminalFixture(request.Label)
		r.Status = "passed"
		var err error
		if request.Label == "second" {
			r.Status, err = "failed", errors.New("SQL failed")
		}
		if writeErr := writeReport(request.Options.reportDir, r); writeErr != nil {
			t.Error(writeErr)
		}
		return terminalTaskResult{Dir: request.Options.reportDir, Err: err}
	}
	requests := []terminalRequest{
		{Label: "first", Options: options{reportDir: filepath.Join(root, "01-first"), password: "private-password"}},
		{Label: "second", Options: options{reportDir: filepath.Join(root, "02-second"), password: "private-password"}},
		{Label: "third", Options: options{reportDir: filepath.Join(root, "03-third"), password: "private-password"}},
	}
	m.startTasks(requests)
	task := m.task
	select {
	case <-task.finished:
	case <-time.After(3 * time.Second):
		t.Fatal("batch did not finish")
	}
	result := <-task.done
	if strings.Join(order, ",") != "first,second,third" || len(result.Outcomes) != 3 || result.Err == nil {
		t.Fatal("independent failure stopped remaining tests or was lost")
	}
	body, err := os.ReadFile(filepath.Join(root, "batch.json"))
	if err != nil || strings.Contains(string(body), "private-password") {
		t.Fatal("batch summary missing or leaks credentials", err)
	}
	var snapshot struct {
		Items []struct {
			Status, ReportDir string
		} `json:"items"`
	}
	if json.Unmarshal(body, &snapshot) != nil || len(snapshot.Items) != 3 || snapshot.Items[1].Status != "failed" || snapshot.Items[2].Status != "passed" {
		t.Fatal("saved outcomes do not preserve individual results")
	}
	m.Update(terminalTaskEvent{Result: &result})
	if !m.batchView || m.batch == nil || len(m.datasets) != 4 {
		t.Fatal("completed batch is not reachable alongside history")
	}
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 40}} {
		m.width, m.height, m.color = size[0], size[1], true
		for i := range m.batch.outcomes {
			m.batch.selected = i
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "› "+m.batch.outcomes[i].Name) || !strings.Contains(view, "Enter 看报告") || len(strings.Split(view, "\n")) > size[1] {
				t.Fatal("batch selection is hidden after resize", size, view)
			}
			for _, line := range strings.Split(view, "\n") {
				if runewidth.StringWidth(line) > size[0] {
					t.Fatal("batch screen overflow")
				}
			}
		}
	}
	m.openBatchReport(1)
	if m.batchView || m.doc == nil || m.doc.View.Dataset != "second" || m.doc.View.Status != "failed" {
		t.Fatal("summary opened a stale report")
	}
	terminalKey(m, "t")
	if !m.batchView {
		t.Fatal("cannot return to this batch from its report")
	}
	after, _ := os.ReadFile(oldPath)
	if string(before) != string(after) {
		t.Fatal("batch changed historical measurements")
	}
}

func TestBatchCancelStopsPendingAfterCleanupAndDisconnect(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		m := launchTestModel(t)
		started, cleaning, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		root := t.TempDir()
		m.executeTask = func(ctx context.Context, request terminalRequest) terminalTaskResult {
			if calls.Add(1) == 1 {
				r := terminalFixture("completed")
				r.Status = "passed"
				if err := writeReport(request.Options.reportDir, r); err != nil {
					t.Error(err)
				}
				return terminalTaskResult{Dir: request.Options.reportDir}
			}
			close(started)
			<-ctx.Done()
			close(cleaning)
			<-release
			return terminalTaskResult{Err: ctx.Err()}
		}
		requests := []terminalRequest{
			{Label: "completed", Options: options{reportDir: filepath.Join(root, "01-completed")}},
			{Label: "cancelled", Options: options{reportDir: filepath.Join(root, "02-cancelled")}},
			{Label: "pending", Options: options{reportDir: filepath.Join(root, "03-pending")}},
		}
		m.startTasks(requests)
		task := m.task
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("batch did not reach second project")
		}
		closed := make(chan struct{})
		if disconnect {
			go func() { m.closeTask(); close(closed) }()
		} else {
			m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		}
		select {
		case <-cleaning:
		case <-time.After(time.Second):
			t.Fatal("cancellation did not reach active project")
		}
		select {
		case <-task.finished:
			t.Fatal("batch exited before active project cleanup")
		default:
		}
		close(release)
		select {
		case <-task.finished:
		case <-time.After(time.Second):
			t.Fatal("batch could not save cancellation outcomes")
		}
		if disconnect {
			<-closed
		}
		result := <-task.done
		if calls.Load() != 2 || len(result.Outcomes) != 3 || !result.Outcomes[2].NotStarted || !errors.Is(result.Err, context.Canceled) {
			t.Fatal("cancel started pending project or lost completed result")
		}
		body, err := os.ReadFile(filepath.Join(root, "batch.json"))
		if err != nil || !strings.Contains(string(body), `"not_started"`) || !strings.Contains(string(body), `"cancelled"`) || !strings.Contains(string(body), `"passed"`) {
			t.Fatal("cancel/disconnect summary lost job states", err)
		}
	}
}
