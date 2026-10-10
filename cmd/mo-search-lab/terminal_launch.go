// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

type terminalPack struct {
	Path, Dataset string
	Rows          int64
	terminalPackInfo
	rank       int
	concurrent bool
}
type launchField struct{ Label, Value string }
type terminalLauncher struct {
	fields                                  []launchField
	focus, cursor                           int
	editing, inspecting, choosing, advanced bool
	packChoice                              int
	concurrencyChoice                       int
	packConcurrency, beforeConcurrency      map[string][]int
	allPacks                                bool
	packs                                   []terminalPack
	selectedPacks, beforePacks              []string
	beforeMixed                             string
	err                                     string
}

const (
	launchKind = iota
	launchHost
	launchPort
	launchUser
	launchPassword
	launchPack
	launchReports
	launchAdvanced
	launchQueryLimit
	launchConcurrency
	launchMixed
	launchTimeout
	launchRepeat
	launchWarmup
	launchStabilityRepeat
	launchStabilityLimit
	launchEnvironment
	launchConfigs
	launchMonitoring
	launchKeepDB
	launchStart
)

// Preview only bounded manifests. Full validation and hashes happen after Start.
func discoverTerminalPacks(root string) ([]terminalPack, error) {
	var packs []terminalPack
	visited := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > 4096 {
			return fmt.Errorf("数据包目录超过 4096 项，请指定更小的 --packs 目录")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() && len(strings.Split(rel, string(filepath.Separator))) > 4 {
			return filepath.SkipDir
		}
		if entry.Name() != "manifest.json" || !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > 1<<20 || len(packs) >= 128 {
			return fmt.Errorf("数据包清单或数量超限")
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var m manifest
		if json.Unmarshal(body, &m) != nil || !safeIdentifier.MatchString(m.Dataset) {
			return nil
		}
		var rows int64
		for _, load := range m.Loads {
			if load.Rows > 0 {
				rows += load.Rows
			}
		}
		p := terminalPack{Path: filepath.Dir(path), Dataset: m.Dataset, Rows: rows}
		describeTerminalPack(&p, m)
		packs = append(packs, p)
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	sort.Slice(packs, func(i, j int) bool {
		if packs[i].Featured != packs[j].Featured {
			return packs[i].Featured
		}
		if packs[i].rank != packs[j].rank {
			return packs[i].rank < packs[j].rank
		}
		if packs[i].Dataset != packs[j].Dataset {
			return packs[i].Dataset < packs[j].Dataset
		}
		return packs[i].Path < packs[j].Path
	})
	return packs, err
}

func (m *terminalModel) openLauncher() {
	if m.task != nil {
		return
	}
	packs, err := discoverTerminalPacks(m.packsRoot)
	o := m.launchSeed
	levels := ""
	if !o.defaultConcurrency {
		for _, c := range o.concurrencyLevels {
			if levels != "" {
				levels += ","
			}
			levels += strconv.Itoa(c)
		}
	}
	m.launcher = &terminalLauncher{packs: packs, inspecting: m.launchInspect || len(packs) == 0 && o.pack == "", fields: []launchField{
		{"任务", ""}, {"SQL 地址", o.host}, {"SQL 端口", strconv.Itoa(o.port)}, {"账号", o.user},
		{"密码", o.password}, {"数据包", o.pack}, {"报告目录", m.reportsRoot},
		{"更多设置", ""}, {"每场景查询数（0 = 全部）", strconv.Itoa(o.queryLimit)},
		{"并发（留空 = 自动）", levels}, {"同时运行的两路场景（可选）", strings.Join(o.mixedScenarios, ",")},
		{"单查询超时", o.timeout.String()}, {"每条查询测量次数", strconv.Itoa(o.repeat)},
		{"预热次数", strconv.Itoa(o.warmup)}, {"稳定性重复次数", strconv.Itoa(o.stabilityRepeat)},
		{"稳定性查询数", strconv.Itoa(o.stabilityQueryLimit)}, {"环境声明 JSON（可选）", o.environmentFile},
		{"MO TOML（可选，逗号分隔）", strings.Join(o.moConfigs, ",")}, {"监控配置 JSON（可选）", o.monitoringConfig},
		{"保留测试库", strconv.FormatBool(o.keepDB)}, {"开始运行", ""},
	}}
	f := m.launcher
	f.packConcurrency = cloneTerminalConcurrency(m.launchConcurrency)
	selected := m.launchPacks
	if selected == nil {
		if o.pack != "" {
			selected = []string{o.pack}
		} else {
			for _, index := range f.packIndices() {
				selected = append(selected, f.packs[index].Path)
			}
		}
	}
	f.setSelectedPacks(selected)
	if len(selected) == 1 && len(o.mixedScenarios) > 0 {
		f.fields[launchMixed].Value = strings.Join(o.mixedScenarios, ",")
	}
	if err != nil {
		m.launcher.err = err.Error()
	}
	m.chooser = false
}

func (f *terminalLauncher) clearPassword() { f.fields[launchPassword].Value = "" }

func (f *terminalLauncher) visible() []int {
	var indices []int
	for i := range f.fields {
		if i == launchConcurrency {
			continue // Per-dataset checkboxes own the execution profile.
		}
		if i == launchMixed && len(f.selectedPacks) != 1 {
			continue
		}
		if f.inspecting && (i == launchPack || i >= launchQueryLimit && i <= launchStabilityLimit && i != launchTimeout || i == launchKeepDB) {
			continue
		}
		if !f.advanced && i > launchAdvanced && i < launchStart {
			continue
		}
		indices = append(indices, i)
	}
	return indices
}

func (f *terminalLauncher) move(delta int) {
	indices := f.visible()
	for i, index := range indices {
		if index == f.focus {
			f.focus = indices[(i+delta+len(indices))%len(indices)]
			return
		}
	}
	f.focus = indices[0]
}

func (f *terminalLauncher) request(seed options) (terminalRequest, error) {
	o := seed
	text := func(i int) string { return strings.TrimSpace(f.fields[i].Value) }
	o.host, o.user = text(launchHost), text(launchUser)
	var err error
	o.port, err = strconv.Atoi(text(launchPort))
	if err != nil || o.port < 1 || o.port > 65535 || o.host == "" || o.user == "" {
		return terminalRequest{}, fmt.Errorf("请填写 SQL 地址、1..65535 端口和账号")
	}
	o.timeout, err = time.ParseDuration(text(launchTimeout))
	if err != nil || o.timeout <= 0 || o.timeout > (1<<63-1)/10 {
		return terminalRequest{}, fmt.Errorf("超时需为正数，如 3m")
	}
	o.password = f.fields[launchPassword].Value
	o.environmentFile, o.monitoringConfig = text(launchEnvironment), text(launchMonitoring)
	o.moConfigs = nil
	for _, path := range strings.Split(text(launchConfigs), ",") {
		if strings.TrimSpace(path) != "" {
			o.moConfigs = append(o.moConfigs, strings.TrimSpace(path))
		}
	}
	if text(launchReports) == "" {
		return terminalRequest{}, fmt.Errorf("请填写报告目录")
	}
	prefix := "benchmark-"
	if f.inspecting {
		prefix = "environment-"
	}
	o.reportDir = filepath.Join(text(launchReports), prefix+time.Now().UTC().Format("20060102-150405.000000000"))
	if !f.inspecting {
		o.pack = text(launchPack)
		if o.pack == "" {
			return terminalRequest{}, fmt.Errorf("请至少勾选一项测试；Enter 或 Ctrl-P 打开列表")
		}
		for _, field := range []struct {
			index    int
			target   *int
			min, max int
		}{
			{launchQueryLimit, &o.queryLimit, 0, 1000000000}, {launchRepeat, &o.repeat, 1, 100000},
			{launchWarmup, &o.warmup, 0, 10000}, {launchStabilityRepeat, &o.stabilityRepeat, 0, 100000},
			{launchStabilityLimit, &o.stabilityQueryLimit, 0, 1000000000},
		} {
			value, e := strconv.Atoi(text(field.index))
			if e != nil || value < field.min || value > field.max {
				return terminalRequest{}, fmt.Errorf("%s：请输入 %d..%d", f.fields[field.index].Label, field.min, field.max)
			}
			*field.target = value
		}
		o.defaultConcurrency = true
		o.concurrency, o.concurrencyLevels = 1, []int{1}
		o.mixedScenarios = nil
		if text(launchMixed) != "" {
			for _, id := range strings.Split(text(launchMixed), ",") {
				o.mixedScenarios = append(o.mixedScenarios, strings.TrimSpace(id))
			}
		}
		o.keepDB = text(launchKeepDB) == "true"
	}
	return terminalRequest{Inspect: f.inspecting, Options: o}, nil
}

func (m *terminalModel) updateLauncher(msg tea.KeyMsg) tea.Cmd {
	f, key := m.launcher, msg.String()
	if key == "ctrl+c" {
		f.clearPassword()
		return tea.Quit
	}
	if f.choosing {
		switch key {
		case "esc":
			f.setSelectedPacks(f.beforePacks)
			f.fields[launchMixed].Value = f.beforeMixed
			f.packConcurrency = cloneTerminalConcurrency(f.beforeConcurrency)
			f.choosing = false
		case "up":
			f.movePack(-1)
		case "down":
			f.movePack(1)
		case "a":
			f.allPacks = !f.allPacks
			f.movePack(0)
		case " ":
			if len(f.packs) > 0 {
				if f.concurrencyChoice > 0 {
					f.toggleConcurrency(f.packs[f.packChoice].Path, f.concurrencyChoice)
				} else {
					f.togglePack(f.packs[f.packChoice].Path)
				}
			}
		case "s":
			f.toggleVisiblePacks()
		case "enter":
			if len(f.selectedPacks) == 0 {
				f.err = "请至少勾选一项测试。"
				return nil
			}
			f.err = ""
			f.choosing = false
		}
		return nil
	}
	if f.editing {
		value := []rune(f.fields[f.focus].Value)
		switch key {
		case "enter", "esc":
			f.editing = false
		case "left":
			f.cursor = max(0, f.cursor-1)
		case "right":
			f.cursor = min(len(value), f.cursor+1)
		case "home", "ctrl+a":
			f.cursor = 0
		case "end", "ctrl+e":
			f.cursor = len(value)
		case "ctrl+u":
			value = nil
			f.cursor = 0
		case "backspace":
			if f.cursor > 0 {
				value = append(value[:f.cursor-1], value[f.cursor:]...)
				f.cursor--
			}
		case "delete":
			if f.cursor < len(value) {
				value = append(value[:f.cursor], value[f.cursor+1:]...)
			}
		default:
			if msg.Type == tea.KeyRunes && len(value)+len(msg.Runes) <= 4096 {
				clean := []rune(strings.ReplaceAll(terminalText(string(msg.Runes)), "\n", " "))
				tail := append([]rune{}, value[f.cursor:]...)
				value = append(append(value[:f.cursor], clean...), tail...)
				f.cursor += len(clean)
			}
		}
		f.fields[f.focus].Value = string(value)
		if !f.editing && f.focus == launchPack {
			f.setSelectedPacks([]string{strings.TrimSpace(string(value))})
		}
		return nil
	}
	switch key {
	case "q":
		f.clearPassword()
		return tea.Quit
	case "esc":
		f.clearPassword()
		if len(m.datasets) > 0 {
			m.launcher = nil
		}
	case "up", "shift+tab":
		f.move(-1)
	case "down", "tab":
		f.move(1)
	case "ctrl+p":
		if !f.inspecting {
			f.openPackChooser()
		}
	case "p":
		if f.focus == launchPack {
			f.editing, f.cursor = true, len([]rune(f.fields[launchPack].Value))
		}
	case "enter", " ", "left", "right":
		switch f.focus {
		case launchPack:
			if key == "enter" {
				f.openPackChooser()
			}
		case launchKind:
			f.inspecting = !f.inspecting
		case launchAdvanced:
			f.advanced = !f.advanced
		case launchKeepDB:
			if f.fields[f.focus].Value == "true" {
				f.fields[f.focus].Value = "false"
			} else {
				f.fields[f.focus].Value = "true"
			}
		case launchStart:
			if key == "enter" {
				requests, err := f.requests(m.launchSeed)
				if err != nil {
					f.err = err.Error()
					return nil
				}
				m.launchSeed = requests[0].Options
				m.launchInspect = requests[0].Inspect
				m.launchPacks = append([]string{}, f.selectedPacks...)
				m.launchConcurrency = cloneTerminalConcurrency(f.packConcurrency)
				m.launchSeed.defaultConcurrency = true
				m.launchSeed.concurrency, m.launchSeed.concurrencyLevels = 1, []int{1}
				m.launchSeed.reportDir = ""
				m.reportsRoot = f.fields[launchReports].Value
				f.clearPassword()
				return m.startTasks(requests)
			}
		default:
			if key == "enter" {
				f.editing = true
				f.cursor = len([]rune(f.fields[f.focus].Value))
			}
		}
	}
	return nil
}

func (m *terminalModel) launcherView() string {
	f := m.launcher
	if f.choosing {
		return m.packChooserView()
	}
	header := []terminalRow{m.brandRow(), {Text: "  新任务 / " + terminalFieldGroup(f.focus), Tone: toneHeading}, m.dividerRow()}
	if f.err != "" {
		header = append(header, terminalRow{Text: "  输入错误：" + f.err, Tone: toneWarning})
	} else if m.taskNotice != "" {
		header = append(header, terminalRow{Text: "  " + m.taskNotice, Tone: toneMuted})
	}
	start := terminalRow{Text: "    [ 开始运行 ]", Tone: toneHeading}
	if f.focus == launchStart {
		start.Text, start.Tone = "  › [ 开始运行 ]  Enter 确认", toneFocus
	}
	hint := "  " + f.fieldHint()
	keys := "  ↑↓ / Tab 选择  Enter 编辑  q 退出"
	if f.editing {
		keys = "  ←→ 光标  Ctrl-U 清空  Enter/Esc 完成"
	}
	footer := []terminalRow{m.dividerRow(), start, {Text: hint, Tone: toneMuted}, {Text: keys, Tone: toneMuted}}
	var body []terminalRow
	group := ""
	for _, index := range f.visible() {
		if index == launchStart {
			continue
		}
		next := terminalFieldGroup(index)
		if index == launchAdvanced {
			body = append(body, terminalRow{Field: -1}, m.launchFieldRow(index))
			group = next
			continue
		}
		if next != group {
			if len(body) > 0 {
				body = append(body, terminalRow{Field: -1})
			}
			body = append(body, terminalRow{Text: "  " + next, Tone: toneHeading, Field: -1})
			group = next
		}
		body = append(body, m.launchFieldRow(index))
	}
	capacity := max(1, m.height-len(header)-len(footer))
	selected := len(body) - 1
	for i, row := range body {
		if row.Field == f.focus {
			selected = i
			break
		}
	}
	// Keep the selected field visible, with room for its group and next field.
	startAt := max(0, selected-capacity+2)
	startAt = min(startAt, max(0, len(body)-capacity))
	return m.frame(header, body[startAt:], footer)
}

func (m *terminalModel) launchFieldRow(index int) terminalRow {
	f := m.launcher
	prefix, tone := "    ", toneBody
	if index == f.focus {
		prefix, tone = "  › ", toneFocus
	}
	label := prefix + terminalPad(terminalFieldLabel(index), min(18, max(14, m.width/4))) + "  "
	available := max(1, m.width-runewidth.StringWidth(label)-2)
	value := f.displayValue(index)
	if f.editing && index == f.focus {
		value = f.fields[index].Value
		if index == launchPassword {
			value = strings.Repeat("•", len([]rune(value)))
		}
		runes := []rune(value)
		cursor := min(f.cursor, len(runes))
		start := 0
		for start < cursor && runewidth.StringWidth(string(runes[start:cursor])) > max(1, available-3) {
			start++
		}
		value = string(runes[start:cursor]) + "▏" + string(runes[cursor:])
		if start > 0 {
			value = "…" + value
		}
	} else if index == launchPack || index == launchReports || index == launchConfigs || index == launchEnvironment || index == launchMonitoring {
		value = terminalTail(value, available)
	}
	return terminalRow{Text: label + terminalClip(value, available), Tone: tone, Field: index}
}

func (f *terminalLauncher) fieldHint() string {
	if f.editing {
		if f.focus == launchPassword {
			return "密码已遮蔽；清空后使用空密码。"
		}
		return "编辑 " + terminalFieldLabel(f.focus)
	}
	switch f.focus {
	case launchKind:
		return "Enter 切换性能测试 / 只读检查"
	case launchPassword:
		return "Enter 编辑密码；留空使用空密码"
	case launchPack:
		return "Enter 勾选测试与可选并发；p 指定路径"
	case launchAdvanced:
		return "Enter 展开或收起测量与环境设置"
	case launchConcurrency:
		return "默认 1 并发；在测试列表勾选 4、8 并发"
	case launchQueryLimit:
		return "0 = 全部查询；填正数可缩短测量"
	case launchMixed:
		return "可选；用逗号分隔两路场景 ID"
	case launchStart:
		if f.inspecting {
			return "只读采集环境；结果写入报告目录"
		}
		return fmt.Sprintf("依次运行 %d 项测试，各自保存报告", len(f.selectedPacks))
	}
	return "Enter 编辑 " + terminalFieldLabel(f.focus)
}

func (m *terminalModel) packChooserView() string {
	f := m.launcher
	indices := f.packIndices()
	choices := f.packChoices()
	mode := "常用"
	if f.allPacks || len(indices) == len(f.packs) {
		mode = "全部"
	}
	title := fmt.Sprintf("  勾选测试 / %s %d 项 · 已选 %d 项", mode, len(indices), len(f.selectedPacks))
	if m.width < 70 {
		title = fmt.Sprintf("  勾选测试  已选 %d 项", len(f.selectedPacks))
	}
	header := []terminalRow{m.brandRow(), {Text: title, Tone: toneHeading}, m.dividerRow()}
	if m.height >= 20 {
		header = append(header, terminalRow{Text: "  默认 1 并发；4、8 并发按数据集勾选。", Tone: toneMuted})
	}
	var footer []terminalRow
	var body []terminalRow
	if len(f.packs) == 0 {
		body = append(body, terminalRow{Text: "  未发现数据包；返回后可填写路径。"})
	} else {
		selected := f.packs[f.packChoice]
		footer = append(footer, m.dividerRow(), terminalRow{Text: "  " + selected.Scale, Tone: toneMuted})
		maxLines := 1
		if m.height >= 20 {
			maxLines = 2
		}
		summaryText := selected.Summary
		if f.concurrencyChoice > 0 {
			summaryText = fmt.Sprintf("追加 %d 并发测量；保留 1 并发基线，稳定性检查仍串行。", f.concurrencyChoice)
		}
		summary := terminalWrap(summaryText, m.textWidth())
		for _, line := range summary[:min(maxLines, len(summary))] {
			footer = append(footer, terminalRow{Text: "  " + line})
		}
		if f.allPacks && m.height >= 20 {
			footer = append(footer, terminalRow{Text: "  标识  " + selected.Dataset, Tone: toneMuted}, terminalRow{Text: "  目录  " + terminalTail(selected.Path, m.textWidth()-6), Tone: toneMuted})
		}
		footer = append(footer, terminalRow{Text: "  ↑↓ 移动  空格 勾选  Enter 完成", Tone: toneMuted})
		more := "  s 全选/清空  a 更多  Esc 返回"
		if f.allPacks {
			more = "  s 全选/清空  a 常用  Esc 返回"
		} else if len(indices) == len(f.packs) {
			more = "  s 全选/清空  Esc 返回"
		}
		if f.err != "" {
			more = "  " + f.err
		}
		footer = append(footer, terminalRow{Text: more, Tone: toneMuted})
		capacity := max(1, m.height-len(header)-len(footer))
		position := 0
		for i, choice := range choices {
			if choice.Pack == f.packChoice && choice.Level == f.concurrencyChoice {
				position = i
			}
		}
		start := max(0, position-capacity+1)
		for _, choice := range choices[start:min(len(choices), start+capacity)] {
			p := f.packs[choice.Pack]
			prefix, tone := "    ", toneBody
			if choice.Pack == f.packChoice && choice.Level == f.concurrencyChoice {
				prefix, tone = "  › ", toneFocus
			}
			if choice.Level > 0 {
				checked := "[ ]"
				if f.concurrencyChecked(p.Path, choice.Level) {
					checked = "[x]"
				}
				if !f.packChecked(p.Path) && tone != toneFocus {
					tone = toneMuted
				}
				body = append(body, terminalRow{Text: fmt.Sprintf("%s  %s %d 并发（可选）", prefix, checked, choice.Level), Tone: tone})
				continue
			}
			count := p.listScale()
			checked := "[ ] "
			if f.packChecked(p.Path) {
				checked = "[x] "
			}
			labelWidth := max(4, m.width-12-runewidth.StringWidth(count))
			body = append(body, terminalRow{Text: prefix + checked + terminalPad(p.Name, labelWidth) + "  " + count, Tone: tone})
		}
		// Short menus put the explanation directly below the choices.
		// Long menus retain a fixed detail area while their choices scroll.
		if len(choices) <= capacity {
			body = append(body, footer[:len(footer)-2]...)
			footer = footer[len(footer)-2:]
		}
	}
	if len(f.packs) == 0 {
		footer = append(footer, terminalRow{Text: "  Esc 返回；Enter 后可输入路径", Tone: toneMuted})
	}
	return m.frame(header, body, footer)
}
