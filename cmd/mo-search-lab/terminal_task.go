// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type terminalRequest struct {
	Inspect bool
	Options options
	Label   string
}

type terminalTaskResult struct {
	Dir      string
	Err      error
	Outcomes []terminalTaskOutcome
}

type terminalTaskEvent struct {
	Message string
	Result  *terminalTaskResult
	Item    int
}

type terminalTaskTick struct{ Task *terminalTask }

type terminalTask struct {
	events     chan terminalTaskEvent
	done       chan terminalTaskResult
	finished   chan struct{}
	cancel     context.CancelFunc
	started    time.Time
	stage      string
	logs       []string
	cancelling bool
	labels     []string
	item       int
}

func defaultTerminalOptions() options {
	fs := flag.NewFlagSet("defaults", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o options
	registerRunFlags(fs, &o)
	o.defaultConcurrency = true
	o.concurrencyLevels = []int{1}
	return o
}

// Both entry points call the existing runners and writers. No SQL is performed
// until inputs, profiles and the new report target have been validated.
func executeTerminalTask(ctx context.Context, request terminalRequest) terminalTaskResult {
	o := request.Options
	var p *pack
	var err error
	if !request.Inspect {
		notifyProgress(ctx, "校验数据包与 SHA-256")
		p, err = loadPackContext(ctx, o.pack)
		if err != nil {
			return terminalTaskResult{Err: err}
		}
		o = defaultPackProfile(p.Scenarios, o)
		runs, planErr := planScenarioRuns(p.Scenarios, o)
		if planErr != nil {
			return terminalTaskResult{Err: planErr}
		}
		if err := validateMixedSelection(p.Scenarios, o, runs); err != nil {
			return terminalTaskResult{Err: err}
		}
	}
	if _, err := loadEnvironmentInputs(o); err != nil {
		return terminalTaskResult{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return terminalTaskResult{Err: err}
	}
	if err := ensureReportTarget(o.reportDir); err != nil {
		return terminalTaskResult{Err: err}
	}
	var report Report
	if request.Inspect {
		report, err = inspectEnvironment(ctx, o)
	} else {
		report, err = runBenchmark(ctx, p, o)
	}
	if report.Dataset == "" {
		return terminalTaskResult{Err: err}
	}
	notifyProgress(ctx, "保存原始记录与报告")
	if writeErr := writeReport(o.reportDir, report); writeErr != nil {
		return terminalTaskResult{Err: errors.Join(err, fmt.Errorf("write report: %w", writeErr))}
	}
	return terminalTaskResult{Dir: o.reportDir, Err: err}
}

func waitTerminalTask(task *terminalTask) tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-task.events:
			return event
		case result := <-task.done:
			return terminalTaskEvent{Result: &result}
		}
	}
}

func tickTerminalTask(task *terminalTask) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return terminalTaskTick{task} })
}

func (m *terminalModel) startTask(request terminalRequest) tea.Cmd {
	return m.startTasks([]terminalRequest{request})
}

func (m *terminalModel) startTasks(requests []terminalRequest) tea.Cmd {
	if m.task != nil || len(requests) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	task := &terminalTask{events: make(chan terminalTaskEvent, 64), done: make(chan terminalTaskResult, 1), finished: make(chan struct{}), cancel: cancel, started: time.Now(), stage: "准备任务"}
	for _, request := range requests {
		task.labels = append(task.labels, request.Label)
	}
	task.item = 1
	m.task, m.launcher, m.taskNotice = task, nil, ""
	m.batch, m.batchView = nil, false
	emit := func(event terminalTaskEvent) {
		// A slow terminal cannot block preparation or measured work.
		select {
		case task.events <- event:
		default:
		}
	}
	execute := m.executeTask
	if execute == nil {
		execute = executeTerminalTask
	}
	go func() {
		defer close(task.finished)
		defer cancel()
		result := executeTerminalRequests(ctx, requests, execute, emit)
		task.done <- result
	}()
	return tea.Batch(waitTerminalTask(task), tickTerminalTask(task))
}

// Also runs on terminal disconnect. The runner owns cleanup and report writing;
// the UI must not leave a worker behind or exit in the middle of DROP DATABASE.
func (m *terminalModel) closeTask() {
	if m.task != nil {
		m.task.cancel()
		<-m.task.finished
	}
	if m.launcher != nil {
		m.launcher.clearPassword()
	}
	m.launchSeed.password = ""
}

func (m *terminalModel) finishTask(result terminalTaskResult) {
	cancelling := m.task.cancelling
	m.task.cancel()
	m.task = nil
	if len(result.Outcomes) > 1 {
		m.finishBatch(result, cancelling)
		return
	}
	if result.Err != nil {
		m.taskNotice = "任务存在错误：" + result.Err.Error()
		if cancelling || errors.Is(result.Err, context.Canceled) {
			m.taskNotice = "任务已取消：" + result.Err.Error()
		}
	} else {
		m.taskNotice = "任务完成 · " + filepath.Join(result.Dir, "report.html")
	}
	if result.Dir == "" {
		m.openLauncher()
		return
	}
	index, runIndex, _, err := m.registerTerminalResult(result.Dir)
	if err != nil {
		m.taskNotice += " · 结果读取失败：" + err.Error()
		return
	}
	m.dataset, m.run, m.section = index, runIndex, 0
	m.load()
	if m.doc != nil && m.doc.View.RunKind == "environment_inspection" {
		m.section = 5
	}
}

func (m *terminalModel) taskView() string {
	task := m.task
	status, tone := "正在运行", toneHeading
	if task.cancelling {
		status, tone = "正在取消", toneWarning
	}
	header := []terminalRow{m.brandRow(), {Text: "  任务 / " + status, Tone: tone}, m.dividerRow()}
	if len(task.labels) > 1 {
		label := fmt.Sprintf("%d/%d · %s", task.item, len(task.labels), task.labels[task.item-1])
		for _, line := range terminalKV("测试项目", label, m.textWidth()) {
			header = append(header, terminalRow{Text: "  " + line, Tone: toneHeading})
		}
	}
	for _, line := range terminalKV("已用时", time.Since(task.started).Truncate(time.Second).String(), m.textWidth()) {
		header = append(header, terminalRow{Text: "  " + line})
	}
	for _, line := range terminalKV("当前阶段", terminalProgressLabel(task.stage), m.textWidth()) {
		header = append(header, terminalRow{Text: "  " + line, Tone: toneFocus})
	}
	footer := []terminalRow{{Text: "  Esc / Ctrl-C 取消任务", Tone: toneMuted}, {Text: "  完成后自动打开本次报告", Tone: toneMuted}}
	if task.cancelling {
		footer = []terminalRow{{Text: "  等待当前操作结束、清理和保存报告", Tone: toneWarning}, {Text: "  清理结束后显示本次结果", Tone: toneMuted}}
	}
	// Long stage names can wrap, but must leave room for the cancellation hint.
	header = header[:min(len(header), m.height-len(footer))]
	capacity := max(0, m.height-len(header)-len(footer))
	var body []terminalRow
	if capacity > 2 && len(task.logs) > 1 {
		body = append(body, terminalRow{}, terminalRow{Text: "  最近阶段", Tone: toneHeading})
		logs := task.logs[:len(task.logs)-1]
		for _, message := range logs[max(0, len(logs)-(capacity-2)):] {
			body = append(body, terminalRow{Text: "    " + terminalProgressLabel(message), Tone: toneMuted})
		}
	}
	return m.frame(header, body, footer)
}
