// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/mattn/go-runewidth"
)

type terminalChoice struct{ Dataset, Run int }

type terminalModel struct {
	datasets               []terminalDataset
	histories              []terminalHistory
	warnings               []string
	dataset, run           int
	doc                    *terminalDocument
	loadError              error
	section, previous      int
	percentile, level      int
	scenario, scroll       int
	width, height          int
	color                  bool
	chooser                bool
	environmentDetails     bool
	choice, chooserScroll  int
	reportsRoot, packsRoot string
	launchSeed             options
	launchInspect          bool
	launchPacks            []string
	launchConcurrency      map[string][]int
	launcher               *terminalLauncher
	task                   *terminalTask
	taskNotice             string
	batch                  *terminalBatchSummary
	batchView              bool
	deletion               *terminalReportDeletion
	executeTask            func(context.Context, terminalRequest) terminalTaskResult
}

func runTerminalUI(args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	root := fs.String("reports", "", "浏览和保存运行的报告根目录；省略时扫描当前目录，新任务写入 reports")
	single := fs.String("report-dir", "", "打开单份报告或整次运行目录；按 batch.json 包含同次运行的报告")
	dataset := fs.String("dataset", "", "初始数据集 ID，使用 report.json 中的原始 dataset，不是显示名称")
	section := fs.String("section", "overview", "初始页面：overview, concurrency, quality, stability, sql, environment, help")
	plain := fs.Bool("plain", false, "只读输出所选页面，不进入交互界面或启动测试")
	environmentDetails := fs.Bool("environment-details", false, "环境页展开配置差异、异常节点和采集失败")
	percentile := fs.Int("percentile", 95, "报告延迟分位数：90, 95, 99")
	level := fs.Int("concurrency", 0, "筛选报告已测并发，0 为全部；不控制新任务运行并发")
	scenario := fs.String("scenario-id", "", "SQL 页的初始场景 ID，使用原始场景标识")
	width := fs.Int("width", 100, "纯文本输出宽度，40..240 列；交互界面随终端调整")
	packs := fs.String("packs", "packs", "新任务的数据包根目录，递归发现已安装测试")
	pack := fs.String("pack", "", "仅预选此数据包；省略时勾选全部已安装常用测试")
	launch := fs.Bool("launch", false, "启动即打开新任务表单，需交互式终端；无报告时会自动打开")
	seed := defaultTerminalOptions()
	registerConnectionFlags(fs, &seed)
	registerEnvironmentFlags(fs, &seed)
	configureCommandHelp(fs)
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
	interactive := !*plain && term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) && os.Getenv("TERM") != "dumb"
	if *launch && !interactive {
		return fmt.Errorf("--launch requires an interactive terminal; use run or inspect for scripts")
	}
	datasets, warnings, err := discoverTerminalReports(*root, *single)
	if err != nil {
		if interactive && *single == "" && (errors.Is(err, errNoTerminalReports) || os.IsNotExist(err)) {
			datasets = nil
		} else {
			if len(warnings) > 0 {
				return fmt.Errorf("%w; skipped reports: %s", err, strings.Join(warnings, "; "))
			}
			return err
		}
	}
	m := &terminalModel{datasets: datasets, warnings: warnings, width: *width, height: 32, section: page, percentile: *percentile, environmentDetails: *environmentDetails}
	m.color = interactive && os.Getenv("NO_COLOR") == ""
	m.reportsRoot, m.packsRoot, m.launchSeed = *root, *packs, seed
	if m.reportsRoot == "." {
		m.reportsRoot = "reports"
	}
	m.launchSeed.pack = *pack
	defer m.closeTask()
	if len(datasets) > 0 && *dataset == "" {
		choice := m.historyGroups()[0].Entries[0]
		for _, candidate := range m.choices() {
			if *single != "" && sameTerminalPackPath(datasets[candidate.Dataset].Runs[candidate.Run].Path, filepath.Join(*single, "report.json")) {
				choice = candidate
				break
			}
		}
		m.dataset, m.run = choice.Dataset, choice.Run
	}
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
	if len(m.datasets) > 0 {
		m.load()
	}
	if m.loadError != nil {
		return m.loadError
	}
	if m.doc != nil && m.doc.View.RunKind == "environment_inspection" {
		var explicitSection bool
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "section" {
				explicitSection = true
			}
		})
		if !explicitSection {
			m.section = 5
		}
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
		if m.doc == nil {
			return fmt.Errorf("no saved report for --scenario-id")
		}
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
	if !interactive {
		return m.writePlain(os.Stdout)
	}
	if *launch || len(m.datasets) == 0 {
		m.openLauncher()
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
	m.histories = nil
	m.doc, m.loadError = nil, nil
	m.scroll, m.scenario, m.level = 0, 0, 0
	if len(m.datasets) == 0 {
		return
	}
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

func (m *terminalModel) openReportChooser() {
	m.histories = nil
	m.chooser, m.chooserScroll = true, 0
	_, m.choice, _ = m.currentHistory()
}

// Page navigation stays within the current report and keeps its view filters.
// Metric help is opened explicitly with ?, rather than as a seventh report page.
func (m *terminalModel) moveReportPage(delta int) {
	pages := len(terminalSections) - 1
	page := m.section
	if page == pages {
		page = m.previous
	}
	m.section, m.scroll = (page+delta+pages)%pages, 0
}

func (m *terminalModel) Init() tea.Cmd { return nil }

func (m *terminalModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case terminalTaskEvent:
		if m.task == nil {
			return m, nil
		}
		if msg.Result != nil {
			m.finishTask(*msg.Result)
			return m, nil
		}
		m.task.stage = msg.Message
		if msg.Item > 0 {
			m.task.item = msg.Item
		}
		m.task.logs = append(m.task.logs, msg.Message)
		if len(m.task.logs) > 12 {
			m.task.logs = m.task.logs[len(m.task.logs)-12:]
		}
		return m, waitTerminalTask(m.task)
	case terminalTaskTick:
		if m.task != nil && m.task == msg.Task {
			return m, tickTerminalTask(m.task)
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, min(240, msg.Width)), max(1, msg.Height)
		m.scroll = 0
	case tea.KeyMsg:
		key := msg.String()
		if m.task != nil {
			if key == "esc" || key == "ctrl+c" || key == "q" {
				m.task.cancelling = true
				m.task.cancel()
			}
			return m, nil
		}
		if m.launcher != nil {
			return m, m.updateLauncher(msg)
		}
		if m.deletion != nil {
			return m, m.updateReportDeletion(msg)
		}
		if key == "q" || key == "ctrl+c" {
			return m, tea.Quit
		}
		if key == "l" {
			m.openLauncher()
			return m, nil
		}
		if m.batchView && m.batch != nil {
			return m, m.updateBatchSummary(msg)
		}
		if key == "t" && m.batch != nil {
			m.batchView, m.chooser = true, false
			return m, nil
		}
		if len(m.datasets) == 0 {
			return m, nil
		}
		if m.chooser {
			choices := m.historyGroups()
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
				m.chooseHistory(m.choice)
			case "x", "delete":
				choice := choices[m.choice].Entries[0]
				m.beginReportDeletion(m.datasets[choice.Dataset].Runs[choice.Run])
			}
			return m, nil
		}
		switch key {
		case "d", "r":
			m.openReportChooser()
		case "left", "right":
			delta := 1
			if key == "left" {
				delta = -1
			}
			m.moveRunReport(delta)
		case "tab", "shift+tab":
			delta := 1
			if key == "shift+tab" {
				delta = -1
			}
			m.moveReportPage(delta)
		case "e":
			if m.section == 5 {
				m.environmentDetails = !m.environmentDetails
				m.scroll = 0
			}
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
			if m.section != 0 && m.section != 1 {
				break
			}
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
			if m.section != 1 && m.section != 2 {
				break
			}
			levels := m.levels()
			for i, level := range levels {
				if m.level == level {
					m.level = levels[(i+1)%len(levels)]
					break
				}
			}
			m.scroll = 0
		case "n", "b":
			if m.section == 4 && m.doc != nil && len(m.doc.View.Scenarios) > 0 {
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

func (m *terminalModel) bodyHeight() int { return max(1, m.height-len(m.reportHeaderRows())-2) }

func (m *terminalModel) headerLines() []string {
	rows := m.reportHeaderRows()
	lines := make([]string, len(rows))
	for i, row := range rows {
		lines[i] = terminalClip(row.Text, m.width)
	}
	return lines
}

func (m *terminalModel) reportHeaderRows() []terminalRow {
	rows := []terminalRow{m.brandRow()}
	if len(m.datasets) == 0 {
		return append(rows, terminalRow{Text: "  检索现场报告", Tone: toneHeading}, m.dividerRow())
	}
	dataset := m.datasets[m.dataset]
	run := dataset.Runs[m.run]
	history, _, reportIndex := m.currentHistory()
	status := "断言通过"
	if m.doc != nil && m.doc.View.MeasurementMode == "observe" {
		status = "测量完成"
	}
	if m.doc != nil && m.doc.View.RunKind == "environment_inspection" {
		status = "SQL 入口可访问"
	}
	if run.Status != "passed" {
		status = "存在未通过检查"
		if m.doc != nil && m.doc.View.MeasurementMode == "observe" {
			status = "运行有错误"
		}
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
	label := "检索报告"
	inspection := m.doc != nil && m.doc.View.RunKind == "environment_inspection"
	if inspection {
		label = "环境报告"
	}
	title := label + " / " + run.DisplayName()
	if runewidth.StringWidth(title) > m.textWidth() {
		title = fmt.Sprintf("%d/%d %s", reportIndex+1, len(history.Entries), run.DisplayName())
	}
	rows = append(rows, terminalRow{Text: "  " + title, Tone: toneHeading})
	tone := toneSuccess
	if run.Status != "passed" {
		tone = toneWarning
	}
	rows = append(rows, terminalRow{Text: "  原始整体判定: " + status, Tone: tone})
	if m.height >= 18 {
		caption := fmt.Sprintf("  运行 %s  ·  报告 %d/%d  ·  ←→ 切报告", history.StartedAt.Format("01-02 15:04 UTC"), reportIndex+1, len(history.Entries))
		if !inspection {
			switch m.section {
			case 0:
				caption += fmt.Sprintf("  ·  P%d  ·  并发 1", m.percentile)
			case 1:
				caption += fmt.Sprintf("  ·  P%d  ·  并发筛选 %s", m.percentile, level)
			case 2:
				caption += "  ·  并发筛选 " + level
			case 3:
				caption += "  ·  并发 1"
			case 4:
				if scenario := m.selectedScenario(); scenario != nil {
					caption += fmt.Sprintf("  ·  所选场景并发 %d", scenario.EffectiveConcurrency)
				}
			}
		}
		rows = append(rows, terminalRow{Text: caption, Tone: toneMuted})
		rows = append(rows, terminalRow{Text: "  原始数据  " + terminalTail(run.Path, m.textWidth()-10), Tone: toneMuted})
	}
	navigation := "页面  " + strings.Join(tabs, "  ")
	if runewidth.StringWidth(navigation) > m.textWidth() {
		navigation = tabs[m.section] + "  ·  Tab 切页"
	}
	rows = append(rows, terminalRow{Text: "  " + navigation, Tone: toneHeading}, m.dividerRow())
	if m.taskNotice != "" {
		rows = append(rows, terminalRow{Text: "  " + m.taskNotice, Tone: toneMuted})
	}
	if len(m.warnings) > 0 {
		rows = append(rows, terminalRow{Text: fmt.Sprintf("  跳过 %d 份报告；环境页查看原因。", len(m.warnings)), Tone: toneWarning})
	}
	return rows
}

func (m *terminalModel) contentLines() []string {
	var content []string
	if m.loadError != nil {
		content = []string{"报告读取失败；按 d 选择其他数据集或运行。", m.loadError.Error()}
	} else if m.doc == nil {
		content = []string{"暂无报告；按 l 配置连接并启动新任务。"}
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
		for _, line := range terminalWrap(block, m.textWidth()) {
			lines = append(lines, "  "+line)
		}
	}
	return lines
}

func (m *terminalModel) View() string {
	if m.width < 40 || m.height < 12 {
		lines := terminalWrap("请将终端调整到至少 40 列 × 12 行；q 退出。", m.width)
		return strings.Join(lines[:min(m.height, len(lines))], "\n")
	}
	if m.task != nil {
		return m.taskView()
	}
	if m.launcher != nil {
		return m.launcherView()
	}
	if m.deletion != nil {
		return m.reportDeletionView()
	}
	if m.batchView && m.batch != nil {
		return m.batchSummaryView()
	}
	header := m.reportHeaderRows()
	footer := m.reportFooter()
	var content []terminalRow
	if m.chooser {
		header, content, footer = m.historyChooserRows()
	} else {
		all := m.contentLines()
		m.scroll = min(m.scroll, max(0, len(all)-m.bodyHeight()))
		for _, line := range all[m.scroll:min(len(all), m.scroll+m.bodyHeight())] {
			tone := toneBody
			if terminalReportHeading(strings.TrimSpace(line)) {
				tone = toneHeading
			}
			content = append(content, terminalRow{Text: line, Tone: tone})
		}
	}
	return m.frame(header, content, footer)
}

func (m *terminalModel) writePlain(w io.Writer) error {
	lines := append(m.headerLines(), m.contentLines()...)
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}
