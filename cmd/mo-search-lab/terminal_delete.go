// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A confirmation pins the report and directory identities. Deletion only unlinks
// flat entries through an open directory handle, never recursively removes a tree.
type terminalReportDeletion struct {
	Run                 terminalRun
	Dir                 string
	ReportInfo, DirInfo os.FileInfo
	Entries             map[string]os.FileInfo
	History             *terminalHistory
	Members             []*terminalReportDeletion
	BatchInfo           os.FileInfo
	Files               int
	Error               string
}

func prepareTerminalReportDeletion(run terminalRun) (*terminalReportDeletion, error) {
	path, err := filepath.Abs(run.Path)
	if err != nil || filepath.Base(path) != "report.json" {
		return nil, fmt.Errorf("只能删除所选 report.json 的报告目录")
	}
	d := &terminalReportDeletion{Run: run, Dir: filepath.Dir(path)}
	if filepath.Dir(d.Dir) == d.Dir {
		return nil, fmt.Errorf("不能删除文件系统根目录")
	}
	d.DirInfo, err = os.Lstat(d.Dir)
	if err != nil || !d.DirInfo.IsDir() {
		return nil, fmt.Errorf("报告目录已变化或不是独立目录")
	}
	d.ReportInfo, err = os.Lstat(path)
	if err != nil || !d.ReportInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("报告文件已变化或不是普通文件")
	}
	header, err := readTerminalHeader(path)
	if err != nil || header.Dataset != run.Dataset || header.ToolVersion != run.ToolVersion || header.Status != run.Status || !header.StartedAt.Equal(run.StartedAt) {
		return nil, fmt.Errorf("报告已变化，请重新打开 CLI 后选择")
	}
	root, err := os.OpenRoot(d.Dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	names, err := terminalReportDeletionEntries(root)
	if err != nil {
		return nil, err
	}
	d.Files = len(names)
	d.Entries = make(map[string]os.FileInfo)
	for _, name := range names {
		d.Entries[name], err = root.Lstat(name)
		if err != nil {
			return nil, err
		}
	}
	return d, nil
}

func terminalReportDeletionEntries(root *os.Root) ([]string, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(1025)
	if err != nil || len(entries) > 1024 {
		return nil, fmt.Errorf("报告目录读取失败或超过 1024 个文件")
	}
	var names []string
	for _, entry := range entries {
		info, err := root.Lstat(entry.Name())
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("目录包含子目录或特殊文件 %q，无法作为单份报告删除", entry.Name())
		}
		name := strings.ToLower(entry.Name())
		if name == "manifest.json" || name == "go.mod" || name == "agents.md" || name == "batch.json" || strings.HasSuffix(name, ".csv") || strings.HasSuffix(name, ".fvecs") || strings.HasSuffix(name, ".ivecs") {
			return nil, fmt.Errorf("目录包含数据包、项目或汇总文件 %q，请使用独立报告目录", entry.Name())
		}
		names = append(names, entry.Name())
	}
	return names, nil
}

func unchangedTerminalReport(info, expected os.FileInfo) bool {
	return info != nil && expected != nil && os.SameFile(info, expected) && info.Mode().IsRegular() && info.Size() == expected.Size() && info.ModTime().Equal(expected.ModTime())
}

// Keep report.json until the sidecars are removed, so a failed deletion still has
// its canonical record. Never follow file symlinks or remove an unexpected child.
func removeTerminalReport(d *terminalReportDeletion) (bool, error) {
	parent, err := os.OpenRoot(filepath.Dir(d.Dir))
	if err != nil {
		return false, err
	}
	defer parent.Close()
	return removeTerminalReportAt(parent, d)
}

func removeTerminalReportAt(parent *os.Root, d *terminalReportDeletion) (bool, error) {
	name := filepath.Base(d.Dir)
	info, err := parent.Lstat(name)
	if err != nil || !info.IsDir() || !os.SameFile(info, d.DirInfo) {
		return false, fmt.Errorf("报告目录已变化；本次未删除")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return false, err
	}
	defer root.Close()
	dir, err := root.Stat(".")
	if err != nil || !os.SameFile(dir, d.DirInfo) {
		return false, fmt.Errorf("报告目录已变化；本次未删除")
	}
	report, err := root.Lstat("report.json")
	if err != nil || !unchangedTerminalReport(report, d.ReportInfo) {
		return false, fmt.Errorf("报告文件已变化；本次未删除")
	}
	names, err := terminalReportDeletionEntries(root)
	if err != nil {
		return false, err
	}
	if err := checkTerminalReportEntries(root, d, names); err != nil {
		return false, err
	}
	for _, file := range names {
		if file != "report.json" {
			if err := root.Remove(file); err != nil {
				return false, fmt.Errorf("删除附带文件 %s: %w", file, err)
			}
		}
	}
	report, err = root.Lstat("report.json")
	if err != nil || !unchangedTerminalReport(report, d.ReportInfo) {
		return false, fmt.Errorf("报告文件已变化，附带文件已移除，原始报告未删除")
	}
	if err := root.Remove("report.json"); err != nil {
		return false, err
	}
	if err := root.Close(); err != nil {
		return true, fmt.Errorf("报告已删除，关闭目录: %w", err)
	}
	info, err = parent.Lstat(name)
	if err != nil || !info.IsDir() || !os.SameFile(info, d.DirInfo) {
		return true, fmt.Errorf("报告已删除，目录位置已变化")
	}
	if err := parent.Remove(name); err != nil {
		return true, fmt.Errorf("报告已删除，目录未清空: %w", err)
	}
	return true, nil
}

func (m *terminalModel) beginReportDeletion(run terminalRun) {
	history := terminalHistory{Dir: filepath.Dir(run.Path), StartedAt: run.StartedAt, FinishedAt: run.FinishedAt}
	var members []terminalRun
	for _, group := range m.historyGroups() {
		for _, choice := range group.Entries {
			if sameTerminalPackPath(m.datasets[choice.Dataset].Runs[choice.Run].Path, run.Path) {
				history = group
				for _, item := range group.Entries {
					members = append(members, m.datasets[item.Dataset].Runs[item.Run])
				}
			}
		}
	}
	if len(members) == 0 {
		members = []terminalRun{run}
	}
	d, err := prepareTerminalHistoryDeletion(history, run, members)
	if err != nil {
		d = &terminalReportDeletion{Run: run, History: &history, Dir: history.Dir, Error: err.Error()}
	}
	m.deletion = d
}

func (m *terminalModel) updateReportDeletion(msg tea.KeyMsg) tea.Cmd {
	switch strings.ToLower(msg.String()) {
	case "esc", "n", "enter":
		m.deletion = nil
	case "q", "ctrl+c":
		m.deletion = nil
		return tea.Quit
	case "y":
		d := m.deletion
		if d.ReportInfo == nil || d.DirInfo == nil {
			return nil
		}
		deleted, complete, err := removeTerminalHistory(d)
		for _, path := range deleted {
			m.forgetTerminalReport(path)
		}
		if !complete {
			d.Error = "删除未完成：" + err.Error()
			if len(deleted) > 0 {
				d.ReportInfo = nil
			}
			return nil
		}
		m.deletion = nil
		m.taskNotice = fmt.Sprintf("已删除整次运行（%d 份报告）。", len(deleted))
		if err != nil {
			m.taskNotice = err.Error()
		}
	}
	return nil
}

func (m *terminalModel) forgetTerminalReport(path string) {
	m.histories = nil
	currentPath, currentIndex := "", 0
	for i, choice := range m.choices() {
		if choice.Dataset == m.dataset && choice.Run == m.run {
			currentPath, currentIndex = m.datasets[choice.Dataset].Runs[choice.Run].Path, i
		}
	}
	var datasets []terminalDataset
	for _, dataset := range m.datasets {
		var runs []terminalRun
		for _, run := range dataset.Runs {
			if !sameTerminalPackPath(run.Path, path) {
				runs = append(runs, run)
			}
		}
		if len(runs) > 0 {
			datasets = append(datasets, terminalDataset{ID: dataset.ID, Runs: runs})
		}
	}
	m.datasets = datasets
	m.histories = nil
	if m.batch != nil {
		for i := range m.batch.outcomes {
			outcome := &m.batch.outcomes[i]
			if sameTerminalPackPath(filepath.Join(outcome.Dir, "report.json"), path) {
				outcome.ReportDeleted = true
			}
		}
	}
	choices := m.choices()
	m.choice = max(0, min(m.choice, len(m.historyGroups())-1))
	if len(choices) == 0 {
		m.dataset, m.run, m.chooser, m.chooserScroll = 0, 0, false, 0
		m.doc, m.loadError = nil, nil
		return
	}
	for _, choice := range choices {
		if sameTerminalPackPath(m.datasets[choice.Dataset].Runs[choice.Run].Path, currentPath) {
			m.dataset, m.run = choice.Dataset, choice.Run
			return
		}
	}
	choice := choices[min(currentIndex, len(choices)-1)]
	m.dataset, m.run = choice.Dataset, choice.Run
	m.load()
}

func (m *terminalModel) reportDeletionView() string {
	d := m.deletion
	enabled := d.ReportInfo != nil && d.DirInfo != nil
	title := "  删除整次运行"
	if !enabled {
		title = "  无法删除整次运行"
	}
	header := []terminalRow{m.brandRow(), {Text: title, Tone: toneWarning}, m.dividerRow()}
	body := []terminalRow{
		{Text: "  " + d.History.StartedAt.Format("2006-01-02 15:04:05 UTC"), Tone: toneHeading},
	}
	if enabled {
		body = append(body, terminalRow{Text: fmt.Sprintf("  删除 %d 份报告及文件，共 %d 个文件。", len(d.Members), d.Files), Tone: toneWarning})
		for _, member := range d.Members {
			body = append(body, terminalRow{Text: "  · " + member.Run.DisplayName(), Tone: toneBody})
		}
	}
	if m.height >= 18 {
		for i, line := range terminalWrap(d.Dir, m.textWidth()-6) {
			prefix := "  目录  "
			if i > 0 {
				prefix = "        "
			}
			body = append(body, terminalRow{Text: prefix + line, Tone: toneMuted})
		}
	} else {
		body = append(body, terminalRow{Text: "  目录  " + terminalTail(d.Dir, m.textWidth()-6), Tone: toneMuted})
	}
	if d.Error != "" {
		for _, line := range terminalWrap(d.Error, m.textWidth()) {
			body = append(body, terminalRow{Text: "  " + line, Tone: toneWarning})
		}
	}
	footer := []terminalRow{{Text: "  y 确认删除  n / Enter / Esc 取消", Tone: toneMuted}, {Text: "  删除后无法恢复。", Tone: toneMuted}}
	if !enabled {
		footer = []terminalRow{{Text: "  Enter / Esc 返回", Tone: toneMuted}, {Text: "  报告保留。", Tone: toneMuted}}
	}
	return m.frame(header, body, footer)
}
