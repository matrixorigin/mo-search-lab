// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type terminalBatchRecord struct {
	SchemaVersion int       `json:"schema_version"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Items         []struct {
		ReportDir string `json:"report_dir"`
		Status    string `json:"status"`
	} `json:"items"`
}

type terminalHistory struct {
	Dir                   string
	StartedAt, FinishedAt time.Time
	Entries               []terminalChoice
	Batch                 bool
	Status                string
}

func readTerminalBatchRecord(dir string) (terminalBatchRecord, error) {
	var batch terminalBatchRecord
	if err := readTerminalJSON(filepath.Join(dir, "batch.json"), 256<<10, &batch); err != nil {
		return batch, err
	}
	if batch.SchemaVersion != 1 || batch.StartedAt.IsZero() || batch.FinishedAt.Before(batch.StartedAt) || len(batch.Items) == 0 || len(batch.Items) > 128 {
		return batch, fmt.Errorf("运行汇总格式或时间无效")
	}
	seen := make(map[string]bool)
	for _, item := range batch.Items {
		name := item.ReportDir
		if name == "" {
			continue // A cancelled/failed item may never have produced a report.
		}
		if name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || seen[name] {
			return batch, fmt.Errorf("运行汇总包含无效或重复的报告目录")
		}
		seen[name] = true
	}
	return batch, nil
}

func (m *terminalModel) historyGroups() []terminalHistory {
	if m.histories != nil {
		return m.histories
	}
	groups := make(map[string]int)
	batches := make(map[string]terminalBatchRecord)
	checked := make(map[string]bool)
	var histories []terminalHistory
	for _, choice := range m.choices() {
		run := m.datasets[choice.Dataset].Runs[choice.Run]
		dir, _ := filepath.Abs(filepath.Dir(run.Path))
		parent := filepath.Dir(dir)
		if !checked[parent] {
			if record, err := readTerminalBatchRecord(parent); err == nil {
				batches[parent] = record
			}
			checked[parent] = true
		}
		batch := batches[parent]
		isBatch := false
		for _, item := range batch.Items {
			if item.ReportDir == filepath.Base(dir) {
				isBatch = true
				break
			}
		}
		h := terminalHistory{Dir: dir, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, Status: "完成"}
		if run.Status != "passed" {
			h.Status = "有未通过记录"
		}
		if isBatch {
			h.Dir, h.Batch, h.StartedAt, h.FinishedAt = parent, true, batch.StartedAt, batch.FinishedAt
			for _, item := range batch.Items {
				if item.Status != "passed" {
					h.Status = "含未完成/错误"
				}
			}
		}
		index, found := groups[h.Dir]
		if !found {
			index = len(histories)
			groups[h.Dir] = index
			histories = append(histories, h)
		}
		histories[index].Entries = append(histories[index].Entries, choice)
	}
	for i := range histories {
		h := &histories[i]
		batch := batches[h.Dir]
		order := make(map[string]int)
		for j, item := range batch.Items {
			order[item.ReportDir] = j
		}
		sort.SliceStable(h.Entries, func(a, b int) bool {
			left, right := h.Entries[a], h.Entries[b]
			l := filepath.Base(filepath.Dir(m.datasets[left.Dataset].Runs[left.Run].Path))
			r := filepath.Base(filepath.Dir(m.datasets[right.Dataset].Runs[right.Run].Path))
			return order[l] < order[r]
		})
	}
	sort.Slice(histories, func(i, j int) bool {
		if histories[i].StartedAt.Equal(histories[j].StartedAt) {
			return histories[i].Dir < histories[j].Dir
		}
		return histories[i].StartedAt.After(histories[j].StartedAt)
	})
	m.histories = histories
	return histories
}

func (m *terminalModel) currentHistory() (terminalHistory, int, int) {
	for i, history := range m.historyGroups() {
		for j, choice := range history.Entries {
			if choice.Dataset == m.dataset && choice.Run == m.run {
				return history, i, j
			}
		}
	}
	return terminalHistory{}, 0, 0
}

func (m *terminalModel) chooseHistory(index int) {
	histories := m.historyGroups()
	if index < 0 || index >= len(histories) {
		return
	}
	history := histories[index]
	choice := history.Entries[0]
	// Keep the same dataset when moving between two whole runs, if available.
	for _, candidate := range history.Entries {
		if candidate.Dataset == m.dataset {
			choice = candidate
			break
		}
	}
	m.dataset, m.run, m.chooser = choice.Dataset, choice.Run, false
	if m.section == 6 {
		m.section = m.previous
	}
	m.load()
}

func (m *terminalModel) moveRunReport(delta int) {
	history, _, index := m.currentHistory()
	if len(history.Entries) <= 1 {
		return
	}
	choice := history.Entries[(index+delta+len(history.Entries))%len(history.Entries)]
	m.dataset, m.run = choice.Dataset, choice.Run
	m.load()
}

func (h terminalHistory) label() string {
	label := fmt.Sprintf("%s · %d 份报告", h.StartedAt.Format("2006-01-02 15:04"), len(h.Entries))
	if h.FinishedAt.After(h.StartedAt) {
		seconds := int(h.FinishedAt.Sub(h.StartedAt).Seconds())
		label += fmt.Sprintf(" · %d:%02d", seconds/60, seconds%60)
	}
	return label
}

func (m *terminalModel) historyChooserRows() ([]terminalRow, []terminalRow, []terminalRow) {
	histories := m.historyGroups()
	header := []terminalRow{m.brandRow(), {Text: "  选择历史运行 · 时间 UTC", Tone: toneHeading}, m.dividerRow()}
	footer := []terminalRow{{Text: "  ↑↓ 选择  Enter 打开  x 删除整次", Tone: toneMuted}, {Text: fmt.Sprintf("  第 %d/%d 次  Esc 返回  q 退出", m.choice+1, len(histories)), Tone: toneMuted}}
	if len(histories) == 0 {
		return header, nil, footer
	}
	m.choice = max(0, min(m.choice, len(histories)-1))
	selected := histories[m.choice]
	if m.height >= 18 {
		var names []string
		for _, choice := range selected.Entries {
			names = append(names, m.datasets[choice.Dataset].Runs[choice.Run].DisplayName())
		}
		lines := terminalWrap(strings.Join(names, " / "), m.textWidth())
		for _, line := range lines[:min(2, len(lines))] {
			header = append(header, terminalRow{Text: "  " + line, Tone: toneMuted})
		}
	}
	capacity := max(1, m.height-len(header)-len(footer))
	m.chooserScroll = max(min(m.chooserScroll, m.choice), m.choice-capacity+1)
	var rows []terminalRow
	for i := m.chooserScroll; i < min(len(histories), m.chooserScroll+capacity); i++ {
		prefix, tone := "    ", toneBody
		if i == m.choice {
			prefix, tone = "  › ", toneFocus
		}
		text := prefix + histories[i].label()
		if m.width < 65 {
			h := histories[i]
			text = fmt.Sprintf("%s%s  %d 份报告", prefix, h.StartedAt.Format("01-02 15:04"), len(h.Entries))
			if h.FinishedAt.After(h.StartedAt) {
				seconds := int(h.FinishedAt.Sub(h.StartedAt).Seconds())
				text += fmt.Sprintf("  %d:%02d", seconds/60, seconds%60)
			}
		}
		if m.width >= 80 {
			text += " · " + histories[i].Status
		}
		rows = append(rows, terminalRow{Text: text, Tone: tone})
	}
	return header, rows, footer
}
