// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
)

var terminalSections = []string{"overview", "concurrency", "quality", "stability", "sql", "environment", "help"}
var terminalSectionTitles = []string{"概览", "并发", "质量", "稳定性", "SQL", "环境", "指标说明"}

// Never allow SQL, errors, paths or dataset names to inject terminal controls.
func terminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

func terminalClip(s string, width int) string {
	s = strings.ReplaceAll(terminalText(s), "\n", " ")
	return runewidth.Truncate(s, max(1, width), "…")
}

func terminalWrap(s string, width int) []string {
	width = max(1, width)
	lines := make([]string, 0, strings.Count(s, "\n")+1)
	for _, line := range strings.Split(terminalText(s), "\n") {
		var part strings.Builder
		used := 0
		for _, r := range line {
			w := runewidth.RuneWidth(r)
			if used+w > width && part.Len() > 0 {
				lines = append(lines, part.String())
				part.Reset()
				used = 0
			}
			part.WriteRune(r)
			used += w
		}
		lines = append(lines, part.String())
	}
	return lines
}

type terminalBar struct {
	Label  string
	Value  float64
	Sample bool
}

func terminalBars(title, unit string, bars []terminalBar, width int, fixedMaximum float64) []string {
	maximum := fixedMaximum
	for _, bar := range bars {
		if bar.Sample {
			maximum = math.Max(maximum, bar.Value)
		}
	}
	lines := []string{title}
	precision := 3
	if unit == "0–1" {
		precision = 4
	}
	if len(bars) == 0 {
		return append(lines, "  未测量")
	}
	labelWidth := min(30, max(12, width/3))
	valueWidth := 5
	for _, bar := range bars {
		if bar.Sample {
			valueWidth = max(valueWidth, len(fmt.Sprintf("%.*f", precision, bar.Value)))
		}
	}
	barWidth := max(2, width-labelWidth-valueWidth-7)
	lines = append(lines, fmt.Sprintf("  单位 %s · 共享坐标 0–%.3f", unit, maximum), "")
	for _, bar := range bars {
		label := terminalPad(bar.Label, labelWidth)
		if !bar.Sample {
			lines = append(lines, "  "+label+" │ 暂无成功样本")
			continue
		}
		count := 0
		if maximum > 0 {
			count = min(barWidth, max(0, int(math.Round(bar.Value/maximum*float64(barWidth)))))
		}
		glyph := strings.Repeat("█", count)
		if count == 0 {
			glyph = "·"
		}
		lines = append(lines, fmt.Sprintf("  %s │%-*s  %*.*f", label, barWidth, glyph, valueWidth, precision, bar.Value))
	}
	return append(lines, "")
}

func terminalLatency(s ScenarioReport, percentile int) float64 {
	switch percentile {
	case 90:
		return s.P90MS
	case 99:
		return s.P99MS
	default:
		return s.P95MS
	}
}

func (m *terminalModel) ordinaryScenarios() []ScenarioReport {
	var scenarios []ScenarioReport
	if m.doc == nil {
		return scenarios
	}
	for _, s := range m.doc.View.Scenarios {
		if s.Oracle != "stable_multiset" && (m.level == 0 || s.EffectiveConcurrency == m.level) {
			scenarios = append(scenarios, s)
		}
	}
	sort.SliceStable(scenarios, func(i, j int) bool {
		if scenarios[i].ID != scenarios[j].ID {
			return scenarios[i].ID < scenarios[j].ID
		}
		return scenarios[i].EffectiveConcurrency < scenarios[j].EffectiveConcurrency
	})
	return scenarios
}

func (m *terminalModel) overviewLines() []string {
	latency := make([]terminalBar, 0, len(m.doc.View.Scenarios))
	qps := make([]terminalBar, 0, len(m.doc.View.Scenarios))
	for _, s := range m.doc.View.Scenarios {
		if s.Oracle == "stable_multiset" || s.EffectiveConcurrency != 1 {
			continue
		}
		latency = append(latency, terminalBar{scenarioDisplayName(s.ID), terminalLatency(s, m.percentile), s.SQLSuccesses > 0})
		qps = append(qps, terminalBar{scenarioDisplayName(s.ID), s.QPS, s.SQLSuccesses > 0})
	}
	lines := []string{"客户端并发 1 · 固定显示 C1，不受 c 键筛选（_mixed 为两路同时运行）", ""}
	lines = append(lines, terminalBars(fmt.Sprintf("P%d 延迟", m.percentile), "ms", latency, m.textWidth(), 0)...)
	lines = append(lines, terminalBars("成功查询吞吐", "QPS", qps, m.textWidth(), 0)...)
	return append(lines, "按 2 看全部并发档位，3 看召回/相关性，4 看重复稳定性。", "性能是本次负载观察；原始整体判定在顶部保留。")
}

func (m *terminalModel) concurrencyLines() []string {
	scenarios := m.ordinaryScenarios()
	latency := make([]terminalBar, 0, len(scenarios))
	qps := make([]terminalBar, 0, len(scenarios))
	for _, s := range scenarios {
		label := fmt.Sprintf("%s / C%d", scenarioDisplayName(s.ID), s.EffectiveConcurrency)
		latency = append(latency, terminalBar{label, terminalLatency(s, m.percentile), s.SQLSuccesses > 0})
		qps = append(qps, terminalBar{label, s.QPS, s.SQLSuccesses > 0})
	}
	lines := []string{"客户端并发上限；数值来自相同场景的各档位实测。", ""}
	lines = append(lines, terminalBars(fmt.Sprintf("P%d 延迟", m.percentile), "ms", latency, m.textWidth(), 0)...)
	lines = append(lines, terminalBars("成功查询吞吐", "QPS", qps, m.textWidth(), 0)...)
	lines = append(lines, "场景 / 并发 / 成功SQL / SQL错误 / 断言失败 / 秒 / P90 / P95 / P99 ms")
	observed := m.doc.View.MeasurementMode == "observe"
	if observed {
		lines[len(lines)-1] = "场景 / 并发 / 成功SQL / SQL错误 / 秒 / P90 / P95 / P99 ms"
	}
	for _, s := range m.ordinaryScenarios() {
		latencies := "暂无成功样本"
		if s.SQLSuccesses > 0 {
			latencies = fmt.Sprintf("%.3f / %.3f / %.3f", s.P90MS, s.P95MS, s.P99MS)
		}
		line := fmt.Sprintf("%s / C%d / %d / %d / %d / %.3f / %s", scenarioDisplayName(s.ID), s.EffectiveConcurrency, s.SQLSuccesses, s.SQLFailures, s.AssertionFailures, s.MeasuredSeconds, latencies)
		if observed {
			line = fmt.Sprintf("%s / C%d / %d / %d / %.3f / %s", scenarioDisplayName(s.ID), s.EffectiveConcurrency, s.SQLSuccesses, s.SQLFailures, s.MeasuredSeconds, latencies)
		}
		lines = append(lines, line)
	}
	if len(m.doc.View.Profile.MixedScenarios) > 0 {
		lines = append(lines, "", "_mixed：C 个配对作业，最多同时运行 2C 条 SQL。", "两路分别记录延迟；QPS 共用批次时长，每对等待两路完成。", "配对节奏会改变负载；向量尾延迟降低不代表单路容量提高。")
	}
	return lines
}

func (m *terminalModel) qualityLines() []string {
	lines := make([]string, 0, len(m.doc.View.Scenarios)*8)
	for i, s := range m.doc.View.Scenarios {
		if s.Oracle != "ann_recall" && s.Oracle != "qrels" || m.level != 0 && s.EffectiveConcurrency != m.level {
			continue
		}
		mode := fmt.Sprintf("单查询阈值 %.3f", s.MinScore)
		if s.QualityMode == "observe" {
			mode = "观察，不设验收线"
		}
		lines = append(lines, fmt.Sprintf("%s / C%d / Top-%d / %s", scenarioDisplayName(s.ID), s.EffectiveConcurrency, s.TopK, mode))
		var bars []terminalBar
		if s.Oracle == "ann_recall" {
			bars = append(bars, terminalBar{fmt.Sprintf("Recall@%d", s.TopK), s.MeanScore, s.SQLSuccesses > 0})
		} else {
			rows := buildRetrievalQualityRows([]ScenarioReport{s})
			for _, row := range rows {
				for _, bar := range row.Bars {
					bars = append(bars, terminalBar{bar.Label, bar.Value, row.Samples > 0})
				}
			}
			if len(bars) == 0 {
				bars = []terminalBar{{Label: "nDCG / Recall / MRR"}}
			}
			lines = append(lines, fmt.Sprintf("收益 %s · relevant_grade >= %d", s.NDCGGain, s.RelevantGrade))
		}
		lines = append(lines, terminalBars("检索质量", "0–1", bars, m.textWidth(), 1)...)
		if s.SQLSuccesses > 0 {
			line := fmt.Sprintf("成功 %d / 执行 %d；平均返回 %.2f 条；SQL错误 %d", s.SQLSuccesses, s.Executions, m.doc.Returned[i], s.SQLFailures)
			if s.QualityMode != "observe" {
				line += fmt.Sprintf("；断言失败 %d", s.AssertionFailures)
			}
			lines = append(lines, line, "")
		} else {
			lines = append(lines, "平均返回：暂无成功样本", "")
		}
	}
	if len(lines) == 0 {
		return []string{"当前范围未测量召回率或人工相关性。"}
	}
	return lines
}

func (m *terminalModel) stabilityLines() []string {
	lines := []string{"重复相同输入：集合检查保留重复 ID 数量；顺序检查由场景指定。", "● 通过  ! 断言失败  × SQL错误  · 未执行；每格一组连续重复。", "稳定性按独立并发 1 执行，不随 c 键筛选。", ""}
	if m.doc.View.MeasurementMode == "observe" {
		lines[0] = "记录相同输入的集合与顺序变化，不设稳定性验收线。"
		lines[1] = "● 与基准一致  ! 变化/差异  × SQL错误  · 未执行"
	}
	for _, s := range m.doc.View.StabilityScenarios {
		lines = append(lines, fmt.Sprintf("%s · Top-%d · %d 查询 × %d 次 · 顺序检查 %t", scenarioDisplayName(s.ID), s.TopK, s.SelectedQueries, s.Repetitions, s.CheckOrder))
		lines = append(lines, stabilityChangeSummary(s))
		matrix := buildStabilityMatrix(s)
		if !matrix.HasData {
			lines = append(lines, "未执行重复检查", s.Error, "")
			continue
		}
		lines = append(lines, fmt.Sprintf("每格 %d 次；完整记录见 report.json", matrix.BinSize))
		for _, row := range matrix.Rows {
			cells := make([]string, 0, len(row.Cells))
			for _, cell := range row.Cells {
				symbol := "·"
				switch cell.Class {
				case "same":
					symbol = "●"
				case "assertion":
					symbol = "!"
				case "sql-error":
					symbol = "×"
				}
				cells = append(cells, symbol)
			}
			lines = append(lines, row.ID+"  "+strings.Join(cells, ""))
		}
		if matrix.HiddenQueries > 0 {
			lines = append(lines, fmt.Sprintf("矩阵展示前 30 查询；其余 %d 查询的数值在下方。", matrix.HiddenQueries))
		}
		for _, result := range s.Stability {
			line := fmt.Sprintf("%s：集合 %d 种，顺序 %d 种，最低重合 %.3f，最多变化ID %d", result.QueryID, result.DistinctResults, result.DistinctOrders, result.WorstOverlap, result.MaxChangedIDs)
			if s.QualityMode == "observe" {
				line += fmt.Sprintf("，顺序变化 %d 次，SQL错误 %d", result.ReorderedExecutions, result.Failures)
			} else {
				line += fmt.Sprintf("，顺序变化 %d 次，旧版失败 %d/%d", result.ReorderedExecutions, result.Failures, result.Executions)
			}
			lines = append(lines, line)
		}
		lines = append(lines, "")
	}
	if len(m.doc.View.StabilityScenarios) == 0 {
		lines = append(lines, "本次运行未包含重复结果稳定性场景。")
	}
	if evidence := m.doc.Evidence["stability-tie-analysis.json"]; evidence != "未提供" {
		lines = append(lines, "补充证据 · stability-tie-analysis.json（不改变原始判定）", evidence)
	}
	return lines
}

func (m *terminalModel) selectedScenario() *ScenarioReport {
	if m.doc == nil || len(m.doc.View.Scenarios) == 0 {
		return nil
	}
	return &m.doc.View.Scenarios[m.scenario%len(m.doc.View.Scenarios)]
}

func (m *terminalModel) sqlLines() []string {
	s := m.selectedScenario()
	if s == nil {
		return []string{"没有场景。"}
	}
	lines := []string{fmt.Sprintf("场景 %s / C%d / %s / %s", scenarioDisplayName(s.ID), s.EffectiveConcurrency, s.Route, s.Oracle), "n / b 切换场景；此页不受 c 键筛选。", "", "原始场景标识: " + s.ID, "", "测量 SQL", s.SQL}
	if s.VectorSQL != "" {
		lines = append(lines, "向量 SQL", s.VectorSQL, "全文 SQL", s.FulltextSQL)
	}
	lines = append(lines, "", "会话设置")
	lines = append(lines, s.SessionSQL...)
	lines = append(lines, "", "索引定义")
	lines = append(lines, m.doc.View.IndexSQL...)
	lines = append(lines, "", "场景 SHA-256", s.ScenarioSHA256, "查询 SHA-256", s.QueriesSHA256, "", "逻辑/物理执行计划", s.Plan)
	if s.FulltextPlan != "" {
		lines = append(lines, "全文计划", s.FulltextPlan)
	}
	endpoints := make([]string, 0, len(s.PhysicalPlans))
	for endpoint := range s.PhysicalPlans {
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)
	for _, endpoint := range endpoints {
		lines = append(lines, "物理计划 · "+endpoint, s.PhysicalPlans[endpoint])
	}
	if s.PlanError != "" || s.Error != "" {
		lines = append(lines, "错误", s.PlanError, s.Error)
	}
	return lines
}

func (m *terminalModel) environmentLines() []string {
	r, v := m.doc.View.Report, m.doc.View.EnvironmentView
	lines := terminalSection("版本与部署")
	appendRows := func(rows []environmentRow) {
		for _, row := range rows {
			lines = append(lines, terminalKV(row.Label, row.Value+" ["+row.Source+"]", m.textWidth())...)
		}
	}
	appendRows(v.Summary)
	if v.ClientSummary != "" {
		lines = append(lines, "压测客户端: "+v.ClientSummary)
	}
	if v.Diagnostics.NodeNotice != "" {
		lines = append(lines, v.Diagnostics.NodeNotice)
	}
	lines = append(lines, terminalSection("节点与缓存")...)
	lines = append(lines, v.SnapshotNote, "")
	if len(v.Caches) == 0 {
		lines = append(lines, "未取得存储与缓存配置。")
	} else {
		for _, cache := range v.Caches {
			lines = append(lines, "  "+cache.Node+" / "+cache.Service)
			for _, row := range []environmentRow{{Label: "后端", Value: cache.Backend}, {Label: "内存缓存 / 每节点", Value: cache.Memory}, {Label: "磁盘缓存 / 每节点", Value: cache.Disk}} {
				lines = append(lines, terminalKV(row.Label, row.Value, m.textWidth())...)
			}
			lines = append(lines, "  来源  "+cache.Source, "")
		}
	}
	if len(v.MetadataCaches) > 0 {
		lines = append(lines, terminalSection("Metadata 缓存 · 每节点容量")...)
		for _, cache := range v.MetadataCaches {
			lines = append(lines, terminalKV(cache.Label, cache.Value+" ["+cache.Source+"]", m.textWidth())...)
			lines = append(lines, "  "+cache.Basis)
		}
		lines = append(lines, "环境默认容量按服务端系统总内存推算；运行时调整未确认。")
	}
	lines = append(lines, terminalSection("检索参数 · SQL 初始会话值，场景 SQL 可以覆盖")...)
	for _, group := range v.Retrieval {
		lines = append(lines, "  "+group.Label)
		appendRows(group.Rows)
		lines = append(lines, "")
	}
	if len(v.Retrieval) == 0 {
		lines = append(lines, "未取得 SQL 检索参数。")
	}
	lines = append(lines, terminalSection("资源监控 · "+v.MonitorStatus)...)
	lines = append(lines, v.MonitorNote)
	appendRows(v.Monitoring)
	for _, plot := range m.doc.View.ResourcePlots {
		lines = append(lines, "", plot.Name+" · "+plot.Unit+" · "+plot.Status)
		if plot.Error != "" {
			lines = append(lines, plot.Error)
		}
		for _, series := range plot.Lines {
			lines = append(lines, fmt.Sprintf("%s: %d 样本 · min/mean/max %.3g / %.3g / %.3g", series.Label, series.Samples, series.Minimum, series.Mean, series.Maximum))
		}
	}
	if !m.environmentDetails {
		lines = append(lines, "", "e 展开诊断详情；纯文本可加 --environment-details。")
	} else {
		lines = append(lines, "", "诊断详情")
		for _, node := range v.Diagnostics.Nodes {
			lines = append(lines, node.Label+": "+node.State+" / "+node.Address+" / "+node.ID)
		}
		if len(v.Diagnostics.Configs) > 0 {
			lines = append(lines, "配置差异")
		}
		for _, config := range v.Diagnostics.Configs {
			line := config.Node + " / " + config.Parameter + ": 快照 " + config.Snapshot + " | 默认 " + config.Default
			if config.hasFile {
				line += " | 文件 " + config.File
			}
			lines = append(lines, line+" ["+config.Difference+"]")
		}
		if len(v.Diagnostics.Missing) > 0 {
			lines = append(lines, "采集缺失")
		}
		appendRows(v.Diagnostics.Missing)
		if v.Diagnostics.Count == 0 {
			lines = append(lines, "这份记录没有诊断条目。")
		}
		if r.RunKind != "environment_inspection" {
			profile, _ := json.MarshalIndent(r.Profile, "", "  ")
			lines = append(lines, "", "运行身份与输入记录", "工具版本: "+r.ToolVersion, "二进制 SHA256: "+r.BinarySHA256)
			lines = append(lines, "数据集: "+r.DisplayName(), "原始标识: "+r.Dataset)
			lines = append(lines, "数据清单 SHA256: "+r.ManifestSHA256, "测试库: "+r.Database+" / "+r.Cleanup, "测量参数", string(profile))
			for _, stage := range r.Stages {
				lines = append(lines, fmt.Sprintf("%s: %.3f s / %d 行 / %s", stage.Name, stage.Seconds, stage.Rows, stage.Error))
			}
			for _, input := range r.Inputs {
				lines = append(lines, fmt.Sprintf("%s / %d 行 / SHA256 %s", input.Path, input.Rows, input.SHA256))
			}
		}
		lines = append(lines, "", "e 收起诊断详情。")
	}
	if len(r.Errors) > 0 {
		lines = append(lines, "", "原始错误 / 未通过断言")
		lines = append(lines, r.Errors...)
	}
	return lines
}

const terminalMetricHelp = `指标说明

P90 / P95 / P99：按成功 SQL 的延迟升序取最近秩分位数。P95 表示
95% 的成功测量延迟不超过此值。统计所有成功 SQL 的实测延迟。
SQL 错误没有可用延迟样本，排除在延迟、吞吐和质量均值之外。

QPS：成功查询执行数 / 批次测量秒数。warmup 不计入。
_mixed 两路共用批次时长；每对等两路完成，两路独立统计。
客户端并发 C 是同时在执行的作业上限，不表示 CN 数量。
两路同时运行时最多 2C 条 SQL；没有客户端融合、重排或模型调用。

Recall@K：返回 ID 与冻结精确 Top-K 的交集数 / 真值数。
过滤场景的真值应在同一可见候选集合内重新计算。
稳定的结果仍可能召回不足；质量和稳定性分别检查。
PRE 先按条件限定候选集合再检索；POST 检索后再过滤。
POST 在可见比例低时可能返回不足 K 条，质量页保留平均返回条数。

nDCG@K：排序相关性得分 / 理想排序得分；越接近 1 越好。
DCG 将相关性等级按名次折扣，nDCG 再归一化。收益与相关等级阈值
沿用记录中的 ndcg_gain / relevant_grade；未标注文档视为零收益。
MRR@10：前 10 条中第一个相关答案的排名倒数，未找到记为 0。
MRR 关注第一个答案；nDCG 同时关注多个答案的等级和排序。

重复稳定性：相同输入反复查询，比较 ID 多重集合与可选顺序。
集合种类 > 1 表示返回成员变化；顺序种类 > 1 表示排序变化。
相同距离的向量可能交换位置；没有 ID tie-breaker 的 SQL 不保证
这类并列顺序。补充分析展示原始证据，原始失败判定保持可见。

图表从保存的 JSON 生成，不发 SQL、不访问网络、不改写原始文件。
机器配额不等于实测利用率；缺失监控明确显示为 unavailable。
图表是本次有限负载下的观察，未设置统一性能或质量验收线。

操作：d 选择历史运行，↑↓ 选择、Enter 打开、Esc 返回；
←/→ 切换本次运行内的报告；Tab/Shift-Tab 切换报告页；
1..6 直达，? 打开指标说明；
概览/并发页按 p 切换分位数；并发/质量页按 c 筛选已测并发；
SQL 页按 n/b 切场景；环境页按 e 展开详情；↑↓ 滚动；q 退出。`
