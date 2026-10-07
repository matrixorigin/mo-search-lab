// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

type terminalChoice struct{ Dataset, Run int }

type terminalModel struct {
	datasets              []terminalDataset
	warnings              []string
	dataset, run          int
	doc                   *terminalDocument
	loadError             error
	section, previous     int
	percentile, level     int
	scenario, scroll      int
	width, height         int
	chooser               bool
	choice, chooserScroll int
}

func runTerminalUI(args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	root := fs.String("reports", "", "directory tree containing saved benchmark reports (default .)")
	single := fs.String("report-dir", "", "open a single directory containing report.json")
	dataset := fs.String("dataset", "", "initial dataset ID, exactly as recorded in report.json")
	section := fs.String("section", "overview", "initial page: overview, concurrency, quality, stability, sql, environment, help")
	plain := fs.Bool("plain", false, "print the selected page without interactive terminal controls")
	percentile := fs.Int("percentile", 95, "latency percentile: 90, 95, 99")
	level := fs.Int("concurrency", 0, "show one measured concurrency level (0 shows all)")
	scenario := fs.String("scenario-id", "", "initial scenario for the SQL page")
	width := fs.Int("width", 100, "plain-output width in columns (40..240)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *root != "" && *single != "" || *width < 40 || *width > 240 || (*percentile != 90 && *percentile != 95 && *percentile != 99) || *level < 0 || *level > 128 {
		return fmt.Errorf("invalid ui flags: choose --reports or --report-dir, width 40..240, percentile 90/95/99, concurrency 0..128")
	}
	page := -1
	for i, name := range terminalSections {
		if *section == name {
			page = i
		}
	}
	if page < 0 {
		return fmt.Errorf("unknown --section %q", *section)
	}
	if *root == "" {
		*root = "."
	}
	datasets, warnings, err := discoverTerminalReports(*root, *single)
	if err != nil {
		if len(warnings) > 0 {
			return fmt.Errorf("%w; skipped reports: %s", err, strings.Join(warnings, "; "))
		}
		return err
	}
	m := &terminalModel{datasets: datasets, warnings: warnings, width: *width, height: 32, section: page, percentile: *percentile}
	if *dataset != "" {
		found := false
		for i, ds := range datasets {
			if ds.ID == *dataset {
				m.dataset, found = i, true
				break
			}
		}
		if !found {
			return fmt.Errorf("dataset %q not found; IDs: %s", *dataset, strings.Join(m.datasetIDs(), ", "))
		}
	}
	m.load()
	if m.loadError != nil {
		return m.loadError
	}
	if *level != 0 {
		found := false
		for _, measured := range m.levels() {
			found = found || measured == *level
		}
		if !found {
			return fmt.Errorf("concurrency %d was not measured in this run", *level)
		}
		m.level = *level
	}
	if *scenario != "" {
		found := false
		for i, s := range m.doc.View.Scenarios {
			if s.ID == *scenario && (*level == 0 || s.EffectiveConcurrency == *level) {
				m.scenario, found = i, true
				break
			}
		}
		if !found {
			return fmt.Errorf("scenario %q not found at selected concurrency", *scenario)
		}
	}
	if *plain || !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) || os.Getenv("TERM") == "dumb" {
		return m.writePlain(os.Stdout)
	}
	_, err = tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout), tea.WithAltScreen()).Run()
	return err
}

func (m *terminalModel) datasetIDs() []string {
	ids := make([]string, 0, len(m.datasets))
	for _, dataset := range m.datasets {
		ids = append(ids, dataset.ID)
	}
	return ids
}

func (m *terminalModel) load() {
	m.doc, m.loadError = nil, nil
	m.scroll, m.scenario, m.level = 0, 0, 0
	m.doc, m.loadError = loadTerminalDocument(m.datasets[m.dataset].Runs[m.run])
}

func (m *terminalModel) levels() []int {
	seen := make(map[int]bool)
	if m.doc != nil {
		for _, s := range m.doc.View.Scenarios {
			if s.Oracle != "stable_multiset" {
				seen[s.EffectiveConcurrency] = true
			}
		}
	}
	levels := []int{0}
	for level := range seen {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	return levels
}

func (m *terminalModel) choices() []terminalChoice {
	var choices []terminalChoice
	for i, dataset := range m.datasets {
		for j := range dataset.Runs {
			choices = append(choices, terminalChoice{i, j})
		}
	}
	return choices
}

func (m *terminalModel) Init() tea.Cmd { return nil }

func (m *terminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, min(240, msg.Width)), max(1, msg.Height)
		m.scroll = 0
	case tea.KeyMsg:
		key := msg.String()
		if key == "q" || key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.chooser {
			choices := m.choices()
			switch key {
			case "esc", "d":
				m.chooser = false
			case "down", "j":
				m.choice = min(len(choices)-1, m.choice+1)
			case "up", "k":
				m.choice = max(0, m.choice-1)
			case "pgdown":
				m.choice = min(len(choices)-1, m.choice+m.bodyHeight())
			case "pgup":
				m.choice = max(0, m.choice-m.bodyHeight())
			case "enter":
				choice := choices[m.choice]
				m.dataset, m.run, m.chooser = choice.Dataset, choice.Run, false
				m.load()
			}
			return m, nil
		}
		switch key {
		case "d":
			m.chooser, m.chooserScroll = true, 0
			for i, choice := range m.choices() {
				if choice.Dataset == m.dataset && choice.Run == m.run {
					m.choice = i
				}
			}
		case "left", "right":
			delta := 1
			if key == "left" {
				delta = -1
			}
			m.dataset = (m.dataset + delta + len(m.datasets)) % len(m.datasets)
			m.run = 0
			m.load()
		case "r":
			m.run = (m.run + 1) % len(m.datasets[m.dataset].Runs)
			m.load()
		case "tab", "shift+tab":
			delta := 1
			if key == "shift+tab" {
				delta = -1
			}
			m.section = (m.section + delta + len(terminalSections)) % len(terminalSections)
			m.scroll = 0
		case "1", "2", "3", "4", "5", "6":
			m.section, m.scroll = int(key[0]-'1'), 0
		case "?":
			if m.section == 6 {
				m.section = m.previous
			} else {
				m.previous, m.section = m.section, 6
			}
			m.scroll = 0
		case "p":
			switch m.percentile {
			case 90:
				m.percentile = 95
			case 95:
				m.percentile = 99
			default:
				m.percentile = 90
			}
			m.scroll = 0
		case "c":
			levels := m.levels()
			for i, level := range levels {
				if m.level == level {
					m.level = levels[(i+1)%len(levels)]
					break
				}
			}
			m.scroll = 0
		case "n", "b":
			if m.doc != nil {
				delta := 1
				if key == "b" {
					delta = -1
				}
				m.scenario = (m.scenario + delta + len(m.doc.View.Scenarios)) % len(m.doc.View.Scenarios)
				m.scroll = 0
			}
		case "down", "j":
			m.scroll++
		case "up", "k":
			m.scroll = max(0, m.scroll-1)
		case "pgdown", " ":
			m.scroll += m.bodyHeight()
		case "pgup":
			m.scroll = max(0, m.scroll-m.bodyHeight())
		case "home":
			m.scroll = 0
		case "end":
			m.scroll = max(0, len(m.contentLines())-m.bodyHeight())
		}
	}
	return m, nil
}

func (m *terminalModel) bodyHeight() int { return max(1, m.height-9) }

func (m *terminalModel) headerLines() []string {
	dataset := m.datasets[m.dataset]
	run := dataset.Runs[m.run]
	status := "断言通过"
	if run.Status != "passed" {
		status = "存在未通过检查"
	}
	level := "全部"
	if m.level != 0 {
		level = fmt.Sprint(m.level)
	}
	tabs := make([]string, 0, len(terminalSectionTitles))
	for i, title := range terminalSectionTitles {
		label := fmt.Sprintf("%d %s", i+1, title)
		if i == 6 {
			label = "? " + title
		}
		if i == m.section {
			label = "[" + label + "]"
		}
		tabs = append(tabs, label)
	}
	lines := []string{"MO Search Lab / 检索现场报告 · 终端版 " + version, fmt.Sprintf("数据集 %d/%d · %s", m.dataset+1, len(m.datasets), dataset.ID), fmt.Sprintf("运行 %d/%d · %s · 原始整体判定: %s", m.run+1, len(dataset.Runs), run.StartedAt.Format("2006-01-02 15:04 UTC"), status), strings.Join(tabs, "  "), fmt.Sprintf("P%d · 并发筛选 %s · SQL场景 %d · 跳过报告 %d（环境页可查看）", m.percentile, level, m.scenario+1, len(m.warnings)), "原始数据: " + run.Path, strings.Repeat("─", m.width)}
	for i := range lines {
		lines[i] = terminalClip(lines[i], m.width)
	}
	return lines
}

func (m *terminalModel) contentLines() []string {
	var content []string
	if m.loadError != nil {
		content = []string{"报告读取失败；按 d 或方向键选择其他运行。", m.loadError.Error()}
	} else {
		switch m.section {
		case 0:
			content = m.overviewLines()
		case 1:
			content = m.concurrencyLines()
		case 2:
			content = m.qualityLines()
		case 3:
			content = m.stabilityLines()
		case 4:
			content = m.sqlLines()
		case 5:
			content = m.environmentLines()
			if len(m.warnings) > 0 {
				content = append(content, "", "跳过的报告")
				content = append(content, m.warnings...)
			}
		case 6:
			content = []string{terminalMetricHelp}
		}
	}
	var lines []string
	for _, block := range content {
		lines = append(lines, terminalWrap(block, m.width)...)
	}
	return lines
}

func (m *terminalModel) View() string {
	if m.width < 40 || m.height < 12 {
		lines := terminalWrap("请将终端调整到至少 40 列 × 12 行；q 退出。", m.width)
		return strings.Join(lines[:min(m.height, len(lines))], "\n")
	}
	lines := m.headerLines()
	lines[0] = "\x1b[1;36m" + lines[0] + "\x1b[0m"
	var content []string
	if m.chooser {
		choices := m.choices()
		m.chooserScroll = min(m.chooserScroll, m.choice)
		m.chooserScroll = max(m.chooserScroll, m.choice-m.bodyHeight()+1)
		for i := m.chooserScroll; i < min(len(choices), m.chooserScroll+m.bodyHeight()); i++ {
			choice := choices[i]
			run := m.datasets[choice.Dataset].Runs[choice.Run]
			prefix := "  "
			if i == m.choice {
				prefix = "▶ "
			}
			content = append(content, terminalClip(fmt.Sprintf("%s%s · %s · %s", prefix, run.Dataset, run.StartedAt.Format("2006-01-02 15:04"), run.Status), m.width))
		}
	} else {
		all := m.contentLines()
		m.scroll = min(m.scroll, max(0, len(all)-m.bodyHeight()))
		content = all[m.scroll:min(len(all), m.scroll+m.bodyHeight())]
	}
	lines = append(lines, content...)
	for len(lines) < m.height-2 {
		lines = append(lines, "")
	}
	footer := "d 数据集/运行  ←/→ 切换  Tab 分页  p 分位数  c 并发  ? 说明  q 退出"
	if m.chooser {
		footer = "↑/↓ 选择数据集/历史运行 · Enter 打开 · Esc 返回 · q 退出"
	}
	lines = append(lines, terminalClip(footer, m.width), terminalClip(fmt.Sprintf("↑/↓ j/k PgUp/PgDn 滚动 · r 历史运行 · n/b SQL场景 · 第 %d 行", m.scroll+1), m.width))
	return strings.Join(lines, "\n")
}

func (m *terminalModel) writePlain(w io.Writer) error {
	lines := append(m.headerLines(), m.contentLines()...)
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}
