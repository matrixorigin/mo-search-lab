// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type terminalTaskOutcome struct {
	Name, Dir, ReportStatus string
	Err                     error
	NotStarted              bool
	ReportDeleted           bool
}

type terminalBatchSummary struct {
	outcomes []terminalTaskOutcome
	selected int
	note     string
}

func executeTerminalRequests(ctx context.Context, requests []terminalRequest, execute func(context.Context, terminalRequest) terminalTaskResult, emit func(terminalTaskEvent)) terminalTaskResult {
	started := time.Now().UTC()
	var combined terminalTaskResult
	for i, request := range requests {
		if err := ctx.Err(); err != nil {
			combined.Err = errors.Join(combined.Err, err)
			for _, pending := range requests[i:] {
				combined.Outcomes = append(combined.Outcomes, terminalTaskOutcome{Name: pending.Label, Err: err, NotStarted: true})
			}
			break
		}
		itemCtx := context.WithValue(ctx, progressKey{}, func(message string) {
			emit(terminalTaskEvent{Message: message, Item: i + 1})
		})
		notifyProgress(itemCtx, "开始测试 · "+request.Label)
		result := execute(itemCtx, request)
		if len(requests) == 1 {
			return result
		}
		outcome := terminalTaskOutcome{Name: request.Label, Dir: result.Dir, Err: result.Err}
		if result.Dir != "" {
			if header, err := readTerminalHeader(filepath.Join(result.Dir, "report.json")); err == nil {
				outcome.ReportStatus = header.Status
			} else {
				outcome.Err = errors.Join(outcome.Err, fmt.Errorf("读取报告: %w", err))
			}
		}
		combined.Outcomes = append(combined.Outcomes, outcome)
		if outcome.Err != nil {
			combined.Err = errors.Join(combined.Err, fmt.Errorf("%s: %w", request.Label, outcome.Err))
		}
	}
	if len(requests) > 1 {
		if err := writeTerminalBatchSummary(requests, combined.Outcomes, started); err != nil {
			combined.Err = errors.Join(combined.Err, fmt.Errorf("保存测试汇总: %w", err))
		}
	}
	return combined
}

func terminalOutcomeState(outcome terminalTaskOutcome) (string, string) {
	switch {
	case outcome.NotStarted:
		return "not_started", "未开始"
	case errors.Is(outcome.Err, context.Canceled):
		return "cancelled", "已取消"
	case outcome.Err == nil && outcome.ReportStatus == "passed":
		return "passed", "完成"
	default:
		return "failed", "有错误"
	}
}

// Persist execution outcomes without serializing connection options/credentials.
// Individual report.json files retain their original measurement contract.
func writeTerminalBatchSummary(requests []terminalRequest, outcomes []terminalTaskOutcome, started time.Time) error {
	if requests[0].Options.reportDir == "" {
		return nil // An injected executor may not write reports.
	}
	root := filepath.Dir(requests[0].Options.reportDir)
	for _, request := range requests {
		if request.Options.reportDir == "" || filepath.Dir(request.Options.reportDir) != root {
			return fmt.Errorf("多项测试需要各自位于同一汇总目录下")
		}
	}
	type item struct {
		Name         string `json:"name"`
		Status       string `json:"status"`
		ReportStatus string `json:"report_status,omitempty"`
		ReportDir    string `json:"report_dir,omitempty"`
		Error        string `json:"error,omitempty"`
	}
	doc := struct {
		SchemaVersion int       `json:"schema_version"`
		ToolVersion   string    `json:"tool_version"`
		StartedAt     time.Time `json:"started_at"`
		FinishedAt    time.Time `json:"finished_at"`
		Items         []item    `json:"items"`
	}{SchemaVersion: 1, ToolVersion: version, StartedAt: started, FinishedAt: time.Now().UTC()}
	for _, outcome := range outcomes {
		state, _ := terminalOutcomeState(outcome)
		entry := item{Name: outcome.Name, Status: state, ReportStatus: outcome.ReportStatus}
		if outcome.Dir != "" {
			entry.ReportDir = filepath.Base(outcome.Dir)
		}
		if outcome.Err != nil {
			entry.Error = outcome.Err.Error()
		}
		doc.Items = append(doc.Items, entry)
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(root, "batch.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(body, '\n'))
	return errors.Join(writeErr, f.Close())
}

func (m *terminalModel) registerTerminalResult(dir string) (int, int, terminalRun, error) {
	m.histories = nil
	header, err := readTerminalHeader(filepath.Join(dir, "report.json"))
	if err != nil {
		return 0, 0, header, err
	}
	for i := range m.datasets {
		if m.datasets[i].ID != header.Dataset {
			continue
		}
		for j, run := range m.datasets[i].Runs {
			if sameTerminalPackPath(run.Path, header.Path) {
				return i, j, header, nil
			}
		}
		m.datasets[i].Runs = append([]terminalRun{header}, m.datasets[i].Runs...)
		return i, 0, header, nil
	}
	m.datasets = append(m.datasets, terminalDataset{ID: header.Dataset, Runs: []terminalRun{header}})
	return len(m.datasets) - 1, 0, header, nil
}

func (m *terminalModel) finishBatch(result terminalTaskResult, cancelling bool) {
	m.batch = &terminalBatchSummary{outcomes: append([]terminalTaskOutcome{}, result.Outcomes...)}
	m.batchView = true
	m.doc, m.loadError = nil, nil
	for i := range m.batch.outcomes {
		outcome := &m.batch.outcomes[i]
		if outcome.Dir == "" {
			continue
		}
		_, _, header, err := m.registerTerminalResult(outcome.Dir)
		if err != nil {
			outcome.Err = errors.Join(outcome.Err, err)
		} else {
			outcome.ReportStatus = header.Status
		}
	}
	m.taskNotice = "所选测试已结束；按 t 查看本次结果。"
	if cancelling || errors.Is(result.Err, context.Canceled) {
		m.taskNotice = "测试已取消；已生成的报告保留，后续项目未启动。"
	} else if result.Err != nil {
		m.taskNotice = "测试存在错误：" + result.Err.Error()
	}
	if cancelling || result.Err != nil {
		m.batch.note = m.taskNotice
	}
}

func (m *terminalModel) openBatchReport(index int) {
	if m.batch == nil || index < 0 || index >= len(m.batch.outcomes) || m.batch.outcomes[index].Dir == "" || m.batch.outcomes[index].ReportDeleted {
		return
	}
	dataset, run, _, err := m.registerTerminalResult(m.batch.outcomes[index].Dir)
	if err != nil {
		m.batch.outcomes[index].Err = errors.Join(m.batch.outcomes[index].Err, err)
		return
	}
	m.dataset, m.run, m.section = dataset, run, 0
	m.load()
	m.batchView = false
}

func (m *terminalModel) updateBatchSummary(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "down", "j":
		m.batch.selected = min(len(m.batch.outcomes)-1, m.batch.selected+1)
	case "up", "k":
		m.batch.selected = max(0, m.batch.selected-1)
	case "enter":
		m.openBatchReport(m.batch.selected)
	case "esc":
		for i, outcome := range m.batch.outcomes {
			if outcome.Dir != "" && !outcome.ReportDeleted {
				m.openBatchReport(i)
				break
			}
		}
	case "d":
		if len(m.datasets) > 0 {
			m.batchView = false
			if m.doc == nil {
				m.dataset, m.run = 0, 0
				m.load()
			}
			m.openReportChooser()
		}
	}
	return nil
}

func (m *terminalModel) batchSummaryView() string {
	batch := m.batch
	passed, finished := 0, 0
	for _, outcome := range batch.outcomes {
		state, _ := terminalOutcomeState(outcome)
		if state == "passed" {
			passed++
		}
		if !outcome.NotStarted {
			finished++
		}
	}
	header := []terminalRow{m.brandRow(), {Text: "  本次测试结果", Tone: toneHeading}, {Text: fmt.Sprintf("  已执行 %d/%d 项 · 完成 %d 项", finished, len(batch.outcomes), passed), Tone: toneMuted}, m.dividerRow()}
	if batch.note != "" {
		header = append(header, terminalRow{Text: "  " + batch.note, Tone: toneWarning})
	}
	selected := batch.outcomes[batch.selected]
	detail := "  本项未生成报告。"
	if selected.Dir != "" {
		detail = "  报告  " + terminalTail(selected.Dir, m.textWidth()-6)
	}
	if selected.Err != nil {
		detail = "  " + selected.Err.Error()
	}
	if selected.ReportDeleted {
		detail = "  本项报告已删除。"
	}
	footer := []terminalRow{{Text: detail, Tone: toneMuted}, {Text: "  ↑↓ 选择 Enter 看报告", Tone: toneMuted}, {Text: "  l 新任务  d 历史  q 退出", Tone: toneMuted}}
	capacity := max(1, m.height-len(header)-len(footer))
	start := max(0, batch.selected-capacity+1)
	var body []terminalRow
	for i := start; i < min(len(batch.outcomes), start+capacity); i++ {
		outcome := batch.outcomes[i]
		state, label := terminalOutcomeState(outcome)
		if outcome.ReportDeleted {
			label = "报告已删除"
		}
		prefix, tone := "    ", toneBody
		if state == "failed" || state == "cancelled" {
			tone = toneWarning
		}
		if i == batch.selected {
			prefix, tone = "  › ", toneFocus
		}
		body = append(body, terminalRow{Text: prefix + terminalPad(outcome.Name, m.width-20) + "  " + label, Tone: tone})
	}
	return m.frame(header, body, footer)
}
