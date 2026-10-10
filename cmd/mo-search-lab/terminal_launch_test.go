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
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func launchTestModel(t *testing.T) *terminalModel {
	t.Helper()
	m := &terminalModel{width: 100, height: 32, percentile: 95, reportsRoot: t.TempDir(), packsRoot: "testdata", launchSeed: defaultTerminalOptions()}
	m.openLauncher()
	return m
}

func TestLauncherProfilesAndPassword(t *testing.T) {
	m := launchTestModel(t)
	f := m.launcher
	f.fields[launchPassword].Value = "private-q-密码"
	request, err := f.request(m.launchSeed)
	if err != nil {
		t.Fatal(err)
	}
	o := request.Options
	if request.Inspect || !o.defaultConcurrency || o.queryLimit != 0 || o.repeat != 1 || o.warmup != 5 || o.stabilityRepeat != 30 || o.stabilityQueryLimit != 5 || len(o.concurrencyLevels) != 1 {
		t.Fatalf("default profile changed: %+v", o)
	}
	if strings.Contains(m.View(), "private-q") {
		t.Fatal("password was displayed")
	}
	t.Setenv("MO_BENCH_PASSWORD", "original-env-password")
	if makeSQLConfig(o).Passwd != "private-q-密码" || os.Getenv("MO_BENCH_PASSWORD") != "original-env-password" {
		t.Fatal("interactive password was ignored or mutated process environment")
	}
	f.fields[launchPassword].Value = ""
	empty, err := f.request(m.launchSeed)
	if err != nil || makeSQLConfig(empty.Options).Passwd != "" {
		t.Fatal("clearing password unexpectedly used an environment variable", err)
	}
	partial := []loadedScenario{{scenario: scenario{ID: "filtered", Route: "sql", Oracle: "ann_recall"}, QueriesData: []query{{AllowedIDRanges: [][2]int64{{0, 9}}}}}}
	if got := defaultPackProfile(partial, o); len(got.concurrencyLevels) != 1 || got.concurrencyLevels[0] != 1 {
		t.Fatal("filter pack did not select serial defaults")
	}
	f.toggleConcurrency(f.selectedPacks[0], 4)
	requests, err := f.requests(m.launchSeed)
	if err != nil || requests[0].Options.defaultConcurrency || len(defaultPackProfile(partial, requests[0].Options).concurrencyLevels) != 2 {
		t.Fatal("selected concurrency profile lost", err)
	}
	f.fields[launchPort].Value = "0"
	if _, err = f.request(m.launchSeed); err == nil {
		t.Fatal("invalid port accepted")
	}
	f.fields[launchPort].Value = "6001"
	f.fields[launchTimeout].Value = "0s"
	if _, err = f.request(m.launchSeed); err == nil {
		t.Fatal("invalid timeout accepted")
	}
	f.fields[launchTimeout].Value = "3m"
	f.inspecting = true
	f.fields[launchPack].Value = "missing-pack"
	f.fields[launchConcurrency].Value = "invalid"
	if request, err = f.request(m.launchSeed); err != nil || !request.Inspect {
		t.Fatal("inspection incorrectly requires a pack or query profile", err)
	}
}

func TestLauncherEditingAndViewport(t *testing.T) {
	m := launchTestModel(t)
	f := m.launcher
	f.focus = launchPassword
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q中文")})
	if !f.editing || f.fields[launchPassword].Value != "q中文" {
		t.Fatal("q exited while entering a password")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if f.fields[launchPassword].Value != "q中" {
		t.Fatal("unicode backspace damaged input")
	}
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 40}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, mode := range []string{"editing", "advanced", "packs"} {
			f.editing, f.advanced, f.choosing = mode == "editing", mode == "advanced", mode == "packs"
			m.color = true
			if mode == "editing" {
				f.focus = launchPassword
			}
			if mode == "advanced" {
				f.focus = launchStart
			}
			body := m.View()
			if len(strings.Split(body, "\n")) > size[1] || strings.Contains(body, "q中") {
				t.Fatal("form overflow or leaked password", mode)
			}
			for _, line := range strings.Split(body, "\n") {
				if runewidth.StringWidth(ansi.Strip(line)) > size[0] {
					t.Fatal("form exceeded terminal width")
				}
			}
			if mode == "advanced" && !strings.Contains(body, "开始运行") {
				t.Fatal("focused start action scrolled off screen")
			}
		}
		f.editing, f.advanced, f.choosing = false, true, false
		for _, index := range f.visible() {
			f.focus = index
			if !strings.Contains(ansi.Strip(m.View()), "› "+terminalFieldLabel(index)) && index != launchStart {
				t.Fatalf("focused field %d is hidden at %v", index, size)
			}
		}
		m.color = false
		if strings.Contains(m.View(), "\x1b") {
			t.Fatal("color-disabled screen contains ANSI styles")
		}
	}
}

func TestPackPreviewDoesNotHashDataOrFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "one")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"dataset":"example","loads":[{"rows":1000000,"file":{"path":"absent.csv"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(root, "duplicate")); err != nil {
		t.Fatal(err)
	}
	packs, err := discoverTerminalPacks(root)
	if err != nil || len(packs) != 1 || packs[0].Rows != 1000000 {
		t.Fatalf("preview requires data or follows symlink: %+v %v", packs, err)
	}
	if _, err = loadPackContext(context.Background(), dir); err == nil {
		t.Fatal("invalid preview was treated as validated pack")
	}
}

func TestTaskCancellationWaitsForCleanupAndDoesNotBlockOnProgress(t *testing.T) {
	m := launchTestModel(t)
	started, cleaning, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m.executeTask = func(ctx context.Context, _ terminalRequest) terminalTaskResult {
		for i := 0; i < 1000; i++ {
			notifyProgress(ctx, "progress")
		}
		close(started)
		<-ctx.Done()
		close(cleaning)
		<-release
		return terminalTaskResult{Err: ctx.Err()}
	}
	m.startTask(terminalRequest{})
	task := m.task
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow terminal blocked worker")
	}
	if cmd := m.startTask(terminalRequest{}); cmd != nil || m.task != task {
		t.Fatal("duplicate job started")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || m.task == nil || !m.task.cancelling {
		t.Fatal("UI quit before cleanup")
	}
	select {
	case <-cleaning:
	case <-time.After(time.Second):
		t.Fatal("cancel did not reach runner")
	}
	select {
	case <-task.finished:
		t.Fatal("completed before cleanup finished")
	default:
	}
	close(release)
	select {
	case <-task.finished:
	case <-time.After(time.Second):
		t.Fatal("completion blocked on full progress queue")
	}
	result := <-task.done
	m.Update(terminalTaskEvent{Result: &result})
	if m.task != nil || m.launcher == nil || !strings.Contains(m.taskNotice, "已取消") {
		t.Fatal("cancel outcome hidden")
	}
	// Terminal disconnect has the same ownership rule, without a UI consumer.
	m.executeTask = func(ctx context.Context, _ terminalRequest) terminalTaskResult {
		for i := 0; i < 1000; i++ {
			notifyProgress(ctx, "full")
		}
		<-ctx.Done()
		return terminalTaskResult{Err: ctx.Err()}
	}
	m.startTask(terminalRequest{})
	closed := make(chan struct{})
	go func() { m.closeTask(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("terminal disconnect leaked worker")
	}
}

func TestCompletedTaskOpensOwnResultAndPreservesHistory(t *testing.T) {
	m, oldPath := newTerminalFixtureModel(t)
	m.reportsRoot, m.packsRoot, m.launchSeed = t.TempDir(), "testdata", defaultTerminalOptions()
	before, _ := os.ReadFile(oldPath)
	report := Report{Dataset: "environment-inspection", ToolVersion: "test", Status: "failed", RunKind: "environment_inspection", StartedAt: time.Now().UTC(), Environment: newEnvironmentEvidence(environmentInputs{})}
	dir := filepath.Join(m.reportsRoot, "new")
	if err := writeReport(dir, report); err != nil {
		t.Fatal(err)
	}
	m.task = &terminalTask{cancel: func() {}}
	m.finishTask(terminalTaskResult{Dir: dir, Err: errors.New("connection failed")})
	if m.doc == nil || m.doc.View.Status != "failed" || m.section != 5 || len(m.datasets) != 2 || !strings.Contains(m.taskNotice, "connection failed") {
		t.Fatal("failed inspection did not auto-open its own report")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}) // zero-scenario inspection must not panic.
	after, _ := os.ReadFile(oldPath)
	if string(before) != string(after) {
		t.Fatal("new task altered old report")
	}
}

func TestTaskExecutorCancellationAndInputFailures(t *testing.T) {
	for _, inspect := range []bool{false, true} {
		o := defaultTerminalOptions()
		o.pack = "testdata/smoke"
		o.reportDir = filepath.Join(t.TempDir(), "new")
		o.host, o.port = "127.0.0.1", 1
		password := "secret-not-for-report"
		o.password = password
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx = context.WithValue(ctx, progressKey{}, func(message string) {
			if strings.HasPrefix(message, "连接 MO") {
				cancel()
			}
		})
		result := executeTerminalTask(ctx, terminalRequest{Inspect: inspect, Options: o})
		if !errors.Is(result.Err, context.Canceled) || result.Dir != o.reportDir {
			t.Fatalf("failed/cancelled task lost report: %+v", result)
		}
		body, err := os.ReadFile(filepath.Join(result.Dir, "report.json"))
		if err != nil || strings.Contains(string(body), password) {
			t.Fatal("raw report missing or leaks password", err)
		}
		var report Report
		if err = json.Unmarshal(body, &report); err != nil || report.Status != "failed" {
			t.Fatal("cancel counted as passed", err)
		}
		if _, err = loadTerminalDocument(terminalRun{Path: filepath.Join(result.Dir, "report.json"), Dataset: report.Dataset, Status: report.Status, ToolVersion: report.ToolVersion, StartedAt: report.StartedAt}); err != nil {
			t.Fatal("failed task is not viewable", err)
		}
		// New launches must not overwrite any part of a saved run.
		result = executeTerminalTask(context.Background(), terminalRequest{Inspect: inspect, Options: o})
		if result.Err == nil || result.Dir != "" {
			t.Fatal("existing report target accepted")
		}
		after, _ := os.ReadFile(filepath.Join(o.reportDir, "report.json"))
		if string(body) != string(after) {
			t.Fatal("saved measurements overwritten")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, progressKey{}, func(string) { cancel() })
	if _, err := loadPackContext(ctx, "testdata/smoke"); !errors.Is(err, context.Canceled) {
		t.Fatal("hash validation cannot be cancelled", err)
	}
}
