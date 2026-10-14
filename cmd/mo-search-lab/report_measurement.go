// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import "math"

type measurementBar struct {
	MeasurementRound
	X, Width, Y, Height float64
}
type measurementChart struct {
	Bars    []measurementBar
	Maximum float64
}

func buildMeasurementChart(s ScenarioReport) measurementChart {
	chart := measurementChart{Maximum: 1}
	for _, round := range s.MeasurementRounds {
		chart.Maximum = math.Max(chart.Maximum, round.QPS)
	}
	for index, round := range s.MeasurementRounds {
		step := 348.0 / float64(len(s.MeasurementRounds))
		height := round.QPS / chart.Maximum * 130
		chart.Bars = append(chart.Bars, measurementBar{MeasurementRound: round, X: 64 + float64(index)*step + step*.2, Width: step * .6, Y: 156 - height, Height: height})
	}
	return chart
}
func measurementStopReason(reason string) string {
	switch reason {
	case "deadline":
		return "测量窗口完成"
	case "sample_limit":
		return "样本上限，提前结束"
	case "cancelled":
		return "已取消"
	}
	return reason
}

const measurementHTML = `{{define "measurement"}}{{if .MeasurementRounds}}
<div class="measurement-rounds"><h4>C{{.EffectiveConcurrency}} · 各轮性能测量</h4>
<p class="chart-note">合并吞吐 {{printf "%.2f" .QPS}} QPS · 成功延迟样本 {{.SQLSuccesses}} · 质量样本 {{.QualitySamples}}（覆盖 {{.QualityQueries}} / {{.SelectedQueries}} 条查询）。{{if gt (len .MeasurementRounds) 1}}各轮 QPS 标准差 {{printf "%.2f" .QPSStdDev}} · 变异系数 {{printf "%.1f" .QPSVariation}}%。{{else}}单轮测量，未评估轮间波动。{{end}}</p>
{{$chart := measurementChart .}}<div class="load-chart"><div class="chart-scroll"><svg viewBox="0 0 440 202" role="img" aria-label="各轮 QPS 柱状图">
<line x1="64" x2="412" y1="156" y2="156" class="chart-grid"/><text x="56" y="160" text-anchor="end">0</text><text x="56" y="30" text-anchor="end">{{printf "%.1f" $chart.Maximum}}</text>
{{range $chart.Bars}}<rect x="{{printf "%.2f" .X}}" y="{{printf "%.2f" .Y}}" width="{{printf "%.2f" .Width}}" height="{{printf "%.2f" .Height}}" fill="#193b55"><title>第 {{.Number}} 轮：{{printf "%.2f" .QPS}} QPS · 实测 {{printf "%.3f" .MeasuredSeconds}} 秒 · {{stopReason .StopReason}}</title></rect><text x="{{printf "%.2f" .X}}" y="179">第 {{.Number}} 轮</text>{{end}}
<text x="238" y="199" text-anchor="middle">测量轮次 / QPS</text></svg></div></div>
<details><summary>展开每轮耗时与延迟样本</summary><div class="chart-scroll"><table class="load-table"><tr><th>轮次</th><th>目标 / 实测秒数</th><th>预热秒数 / 错误</th><th>质量处理秒数</th><th>成功 / SQL 错误</th><th>QPS</th><th>P50 / P90 / P95 / P99 ms</th><th>结束原因</th></tr>
{{range .MeasurementRounds}}<tr><td>{{.Number}}</td><td>{{printf "%.3f / %.3f" .TargetSeconds .MeasuredSeconds}}</td><td>{{printf "%.3f" .WarmupSeconds}} / {{.WarmupErrors}}</td><td>{{printf "%.3f" .QualitySeconds}}</td><td>{{.SQLSuccesses}} / {{.SQLFailures}}</td><td>{{printf "%.2f" .QPS}}</td><td>{{printf "%.2f / %.2f / %.2f / %.2f" .P50MS .P90MS .P95MS .P99MS}}</td><td>{{stopReason .StopReason}}</td></tr>{{end}}</table></div></details>
{{range .MeasurementWarnings}}<p class="chart-note">{{.}}</p>{{end}}</div>
{{end}}{{end}}`
