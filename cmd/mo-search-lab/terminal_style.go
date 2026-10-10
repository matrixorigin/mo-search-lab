// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

type terminalTone int

const (
	toneBody terminalTone = iota
	toneHeading
	toneMuted
	toneFocus
	toneSuccess
	toneWarning
)

type terminalRow struct {
	Text  string
	Tone  terminalTone
	Field int
}

func terminalPad(text string, width int) string {
	text = terminalClip(text, width)
	return text + strings.Repeat(" ", max(0, width-runewidth.StringWidth(text)))
}

// Long paths keep their filename; field editing still exposes the full value.
func terminalTail(text string, width int) string {
	text = strings.ReplaceAll(terminalText(text), "\n", " ")
	if runewidth.StringWidth(text) <= width {
		return text
	}
	runes := []rune(text)
	used, start := 1, len(runes)
	for start > 0 && used+runewidth.RuneWidth(runes[start-1]) <= width {
		start--
		used += runewidth.RuneWidth(runes[start])
	}
	return "…" + string(runes[start:])
}

func (m *terminalModel) textWidth() int { return max(1, m.width-4) }

func (m *terminalModel) brandRow() terminalRow {
	left, right := "  MO Search Lab", version+"  "
	return terminalRow{Text: left + strings.Repeat(" ", max(2, m.width-runewidth.StringWidth(left+right))) + right, Tone: toneHeading}
}

func (m *terminalModel) dividerRow() terminalRow {
	return terminalRow{Text: "  " + strings.Repeat("─", m.textWidth()), Tone: toneMuted}
}

func (m *terminalModel) renderRow(row terminalRow) string {
	text := terminalClip(row.Text, m.width)
	if !m.color {
		return text
	}
	style := ""
	switch row.Tone {
	case toneHeading:
		style = "1"
	case toneMuted:
		style = "2"
	case toneFocus:
		style = "1;36"
	case toneSuccess:
		style = "32"
	case toneWarning:
		style = "1;33"
	}
	if style == "" {
		return text
	}
	return "\x1b[" + style + "m" + text + "\x1b[0m"
}

func (m *terminalModel) frame(header, body, footer []terminalRow) string {
	capacity := max(0, m.height-len(header)-len(footer))
	rows := append([]terminalRow{}, header...)
	rows = append(rows, body[:min(len(body), capacity)]...)
	for len(rows) < m.height-len(footer) {
		rows = append(rows, terminalRow{})
	}
	rows = append(rows, footer...)
	if len(rows) > m.height {
		rows = rows[:m.height]
	}
	lines := make([]string, len(rows))
	for i, row := range rows {
		lines[i] = m.renderRow(row)
	}
	return strings.Join(lines, "\n")
}

func terminalFieldLabel(index int) string {
	labels := []string{"执行任务", "地址", "端口", "账号", "密码", "测试项目", "报告目录", "更多设置", "查询数量", "并发档位", "两路同时查询", "单查询超时", "测量次数", "预热次数", "重复稳定性次数", "稳定性查询数", "环境声明", "MO 配置 (TOML)", "监控配置", "保留测试库", "开始运行"}
	return labels[index]
}

func terminalFieldGroup(index int) string {
	switch {
	case index == launchKind:
		return "任务"
	case index <= launchPassword:
		return "连接 MO"
	case index <= launchReports:
		return "数据与报告"
	case index == launchAdvanced:
		return "更多设置"
	case index <= launchStabilityLimit:
		return "测量参数"
	case index <= launchMonitoring:
		return "环境与监控"
	default:
		return "运行操作"
	}
}

func (f *terminalLauncher) displayValue(index int) string {
	value := f.fields[index].Value
	switch index {
	case launchKind:
		if f.inspecting {
			return "环境检查（只读）"
		}
		return "性能测试"
	case launchPassword:
		if value == "" {
			return "(空密码)"
		}
		return strings.Repeat("•", len([]rune(value)))
	case launchAdvanced:
		if f.advanced {
			return "[-] 收起"
		}
		return "[+] 展开"
	case launchKeepDB:
		if value == "true" {
			return "是"
		}
		return "否"
	case launchConcurrency:
		if value == "" {
			return "自动"
		}
	case launchQueryLimit:
		if value == "0" {
			return "全部查询"
		}
	case launchPack:
		return f.selectionLabel()
	}
	if value == "" {
		return "未设置"
	}
	return value
}

func terminalNumber(value int64) string {
	text := strconv.FormatInt(value, 10)
	for at := len(text) - 3; at > 0; at -= 3 {
		text = text[:at] + "," + text[at:]
	}
	return text
}

func terminalProgressLabel(message string) string {
	switch {
	case strings.HasPrefix(message, "stage ddl_"):
		return "建立表结构 · 步骤 " + strings.TrimPrefix(message, "stage ddl_")
	case strings.HasPrefix(message, "stage load_"):
		return "导入数据 · " + strings.TrimPrefix(message, "stage load_")
	case strings.HasPrefix(message, "stage index_"):
		return "建立索引 · 步骤 " + strings.TrimPrefix(message, "stage index_")
	case strings.HasPrefix(message, "run mixed "):
		message = "两路同时查询 · " + strings.TrimPrefix(message, "run mixed ")
	case strings.HasPrefix(message, "run "):
		message = "查询场景 · " + strings.TrimPrefix(message, "run ")
	}
	return strings.NewReplacer("client concurrency=", "并发 ", "paired concurrency=", "并发 ", "repeat=", "测量次数 ", "query-limit=0", "全部查询", "query-limit=", "查询数 ").Replace(message)
}

func terminalKV(label, value string, width int) []string {
	column := min(22, max(10, width/3))
	label = terminalPad(label, column)
	prefix := "  " + label + "  "
	lines := terminalWrap(value, max(1, width-runewidth.StringWidth(prefix)))
	for i, line := range lines {
		if i == 0 {
			lines[i] = prefix + line
		} else {
			lines[i] = strings.Repeat(" ", runewidth.StringWidth(prefix)) + line
		}
	}
	return lines
}

func terminalSection(title string) []string { return []string{"", title, ""} }

func terminalReportHeading(text string) bool {
	for _, prefix := range []string{"P90 延迟", "P95 延迟", "P99 延迟", "成功查询吞吐", "检索质量", "版本与部署", "节点与缓存", "Metadata 缓存", "检索参数", "资源监控", "诊断详情", "测量 SQL", "向量 SQL", "全文 SQL", "会话设置", "索引定义", "场景 SHA", "查询 SHA", "逻辑/物理执行计划", "物理计划", "全文计划", "运行身份与输入记录", "配置差异", "采集缺失", "原始错误", "指标说明"} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func (m *terminalModel) reportFooter() []terminalRow {
	primary := "  d 历史  ←→ 报告  q 退出"
	if m.width >= 70 {
		primary = "  d 历史运行  ←→ 切报告  l 新任务  q 退出"
	}
	if m.width >= 105 {
		primary = "  d 历史运行  ←→ 切报告  Tab 切页  1..6 直达  l 新任务  ? 说明  q 退出"
	}
	if m.batch != nil {
		if m.width >= 105 {
			primary = "  d 历史运行  ←→ 切报告  Tab 切页  t 本次测试  l 新任务  q 退出"
		}
	}
	context := "  ↑↓ 滚动  p 分位数"
	switch m.section {
	case 1:
		context = "  ↑↓ 滚动  p 分位数  c 并发"
	case 2:
		context = "  ↑↓ 滚动  c 并发  ? 指标说明"
	case 3:
		context = "  ↑↓ 滚动  ? 稳定性说明"
	case 4:
		context = "  ↑↓ 滚动  n/b SQL 场景"
	case 5:
		context = "  ↑↓ 滚动  e 诊断详情"
	case 6:
		context = "  ↑↓ 滚动  ? 返回报告"
	}
	if m.width >= 70 && m.width < 105 {
		context = "  Tab 切页  " + strings.TrimSpace(context)
	}
	if m.width >= 70 {
		context += fmt.Sprintf("   · 第 %d 行", m.scroll+1)
	}
	return []terminalRow{{Text: primary, Tone: toneMuted}, {Text: context, Tone: toneMuted}}
}
