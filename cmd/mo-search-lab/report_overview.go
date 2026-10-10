// Copyright 2026 Matrix Origin
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type overviewPoint struct {
	X, Y, Value float64
	Level       int
	Tooltip     string
}

type overviewLine struct {
	Label, Class, Path string
	Points             []overviewPoint
}

type overviewTick struct {
	Position float64
	Label    string
}

type overviewChart struct {
	ID, Title, Unit, Note string
	Lines                 []overviewLine
	XTicks, YTicks        []overviewTick
	HasData               bool
}

type chartOverview struct {
	Enabled   bool
	Hidden    int
	QPS       overviewChart
	Latencies []overviewChart
	Qualities []overviewChart
}

func overviewLabel(s concurrencySeries) string {
	label := scenarioDisplayName(s.ID)
	if len(s.SessionSQL) > 0 {
		return label + " · " + strings.TrimPrefix(s.SessionSQL[0], "SET ")
	}
	if label != s.ID {
		return label
	}
	return s.ID + "（默认参数）"
}

func buildChartOverview(groups []concurrencySeries) chartOverview {
	view := chartOverview{Enabled: len(groups) > 0}
	// The full scenario charts remain below. Keep the default overlays readable.
	view.Hidden = max(0, len(groups)-8)
	groups = groups[:min(8, len(groups))]
	view.QPS = makeOverviewChart("qps", "查询吞吐", "QPS", groups, 0, func(p concurrencyPoint) (float64, string, bool) {
		return p.QPS, fmt.Sprintf("成功查询 %d；测量 %.3f 秒", p.SQLSuccesses, p.MeasuredSeconds), p.HasSamples
	})
	view.QPS.Note = "成功执行数 / 测量秒数。混合检索按融合请求计数；质量未过线的成功执行也计入。"
	for _, group := range groups {
		for _, point := range group.Points {
			if point.ExecutionMode == "mixed" {
				view.QPS.Note = "各路成功 SQL 数 / 测量秒数。_mixed 为同时运行；两路共用批次时长，每对等待两路完成，QPS 表示配对负载吞吐。"
			}
		}
	}
	for _, metric := range []string{"P90", "P95", "P99"} {
		chart := makeOverviewChart(strings.ToLower(metric), metric+" 查询延迟", "ms", groups, 0, func(p concurrencyPoint) (float64, string, bool) {
			value := p.P95MS
			if metric == "P90" {
				value = p.P90MS
			} else if metric == "P99" {
				value = p.P99MS
			}
			return value, fmt.Sprintf("成功样本 %d；SQL 错误 %d", p.SQLSuccesses, p.SQLFailures), p.HasSamples
		})
		view.Latencies = append(view.Latencies, chart)
	}
	type qualityGroup struct {
		name, key string
		groups    []concurrencySeries
	}
	var qualities []qualityGroup
	for _, s := range groups {
		key, name := "boolean", "结果断言通过率"
		if s.Points[0].QualityMode == "observe" {
			key, name = "observed_result", "结果匹配度"
		}
		if s.Oracle == "ann_recall" {
			key, name = fmt.Sprint("recall", s.Points[0].TopK), fmt.Sprintf("平均 Recall@%d", s.Points[0].TopK)
		} else if s.Oracle == "qrels" {
			key, name = fmt.Sprint("ndcg", s.Points[0].TopK), fmt.Sprintf("平均 nDCG@%d", s.Points[0].TopK)
		}
		index := -1
		for i := range qualities {
			if qualities[i].key == key {
				index = i
			}
		}
		if index < 0 {
			index = len(qualities)
			qualities = append(qualities, qualityGroup{name: name, key: key})
		}
		qualities[index].groups = append(qualities[index].groups, s)
	}
	for _, quality := range qualities {
		chart := makeOverviewChart(quality.key, quality.name, "0–1", quality.groups, 1, func(p concurrencyPoint) (float64, string, bool) {
			value := p.MeanScore
			if quality.key == "boolean" && p.SQLSuccesses > 0 {
				value = float64(p.SQLSuccesses-p.AssertionFailures) / float64(p.SQLSuccesses)
			}
			if p.QualityMode == "observe" {
				return value, fmt.Sprintf("成功 SQL %d；SQL 错误 %d", p.SQLSuccesses, p.SQLFailures), p.HasSamples
			}
			return value, fmt.Sprintf("单查询断言通过 %d/%d；阈值 %.3f", p.SQLSuccesses-p.AssertionFailures, p.SQLSuccesses, p.MinScore), p.HasSamples
		})
		chart.Note = "均值不代替单查询断言。悬停查看达标数量；执行错误不计入质量均值。"
		if quality.groups[0].Points[0].QualityMode == "observe" {
			chart.Note = "记录实测质量，不设验收阈值；执行错误不计入质量均值。"
		}
		view.Qualities = append(view.Qualities, chart)
	}
	return view
}

func makeOverviewChart(id, title, unit string, groups []concurrencySeries, fixedMaximum float64, metric func(concurrencyPoint) (float64, string, bool)) overviewChart {
	chart := overviewChart{ID: id, Title: title, Unit: unit}
	levels := make(map[int]bool)
	maximum := fixedMaximum
	for _, group := range groups {
		for _, p := range group.Points {
			levels[p.EffectiveConcurrency] = true
			if value, _, valid := metric(p); valid {
				maximum = math.Max(maximum, value)
			}
		}
	}
	ordered := make([]int, 0, len(levels))
	for level := range levels {
		ordered = append(ordered, level)
	}
	sort.Ints(ordered)
	if maximum <= 0 {
		maximum = 1
	} else if fixedMaximum == 0 {
		power := math.Pow(10, math.Floor(math.Log10(maximum)))
		maximum = math.Ceil(maximum/power) * power
	}
	xFor := make(map[int]float64)
	for i, level := range ordered {
		x := 292.0
		if len(ordered) > 1 {
			x = 84 + float64(i)/float64(len(ordered)-1)*416
		}
		xFor[level] = x
		chart.XTicks = append(chart.XTicks, overviewTick{x, fmt.Sprint(level)})
	}
	tickCount := 4

	for i := 0; i <= tickCount; i++ {
		value := maximum * float64(i) / float64(tickCount)
		chart.YTicks = append(chart.YTicks, overviewTick{218 - float64(i)/float64(tickCount)*194, formatOverviewTick(value)})
	}
	for _, group := range groups {
		line := overviewLine{Label: overviewLabel(group), Class: fmt.Sprintf("series-%d", group.ColorIndex)}
		var path strings.Builder
		pen := "M"
		byLevel := make(map[int]concurrencyPoint)
		for _, p := range group.Points {
			byLevel[p.EffectiveConcurrency] = p
		}
		for _, level := range ordered {
			p, exists := byLevel[level]
			value, note, valid := metric(p)
			if !exists || !valid {
				pen = "M"
				continue
			}
			chart.HasData = true
			point := overviewPoint{X: xFor[level], Y: 218 - value/maximum*194, Value: value, Level: level,
				Tooltip: fmt.Sprintf("%s · 并发 %d · %s %.3f %s；%s", line.Label, level, title, value, unit, note)}
			line.Points = append(line.Points, point)
			fmt.Fprintf(&path, "%s%.2f %.2f ", pen, point.X, point.Y)
			pen = "L"
		}
		line.Path = path.String()
		chart.Lines = append(chart.Lines, line)
	}
	return chart
}

func formatOverviewTick(value float64) string {
	for _, unit := range []struct {
		size   float64
		suffix string
	}{{1e9, "G"}, {1e6, "M"}, {1e3, "k"}} {
		if value >= unit.size {
			return fmt.Sprintf("%.3g%s", value/unit.size, unit.suffix)
		}
	}
	return fmt.Sprintf("%.3g", value)
}

const overviewHTML = `<!-- overview:start -->{{if .Overview.Enabled}}<section class="card overview" id="overview"><div class="section-heading"><h2>客户端并发对照</h2><span class="section-kicker">同一份数据 · 同一索引</span></div>
<p class="chart-note">颜色对应测试场景。横轴为客户端并发档位，各档位顺序执行并分别预热。点上悬停可查看原始数值和样本数。</p>{{if .Overview.Hidden}}<p class="chart-note">总览展示前 8 个场景，其余 {{.Overview.Hidden}} 个可展开下方的完整场景图表。</p>{{end}}
<div class="overview-grid"><article class="overview-panel"><h3>查询吞吐 <small>QPS · 越高越好</small></h3>{{template "overviewPlot" .Overview.QPS}}<p class="chart-note">{{.Overview.QPS.Note}}</p></article>
<article class="overview-panel"><div class="percentile-control"><h3>查询延迟 <small>ms · 越低越好</small></h3><input class="metric-radio" type="radio" name="overview-percentile" id="overview-p90"><label for="overview-p90">P90</label><input class="metric-radio" type="radio" name="overview-percentile" id="overview-p95" checked><label for="overview-p95">P95</label><input class="metric-radio" type="radio" name="overview-percentile" id="overview-p99"><label for="overview-p99">P99</label><div class="percentile-views">{{range .Overview.Latencies}}<div class="percentile-view metric-{{.ID}}">{{template "overviewPlot" .}}</div>{{end}}</div></div><p class="chart-note">成功执行的客户端往返耗时；混合检索为完整融合请求耗时。各分位数使用各自标注的零起点时间轴。</p></article>
{{range .Overview.Qualities}}<article class="overview-panel"><h3>{{.Title}} <small>0–1 · 越高越好</small></h3>{{template "overviewPlot" .}}<p class="chart-note">{{.Note}}</p></article>{{end}}
</div></section>{{end}}<!-- overview:end -->
{{define "overviewPlot"}}<div class="overview-legend">{{range .Lines}}<span class="overview-key {{.Class}}"><i></i>{{.Label}}</span>{{end}}</div>{{if .HasData}}<svg class="overview-plot" viewBox="0 0 520 276" role="img" aria-label="{{.Title}}，单位 {{.Unit}}"><title>{{.Title}}</title><g class="overview-axis">{{range .YTicks}}<line x1="84" x2="500" y1="{{printf "%.2f" .Position}}" y2="{{printf "%.2f" .Position}}"/><text x="74" class="y-tick" y="{{printf "%.2f" .Position}}" dy="5" text-anchor="end">{{.Label}}</text>{{end}}{{range .XTicks}}<text x="{{printf "%.2f" .Position}}" y="240" text-anchor="middle">{{.Label}}</text>{{end}}<text x="292" y="267" text-anchor="middle">客户端并发档位</text></g>{{range .Lines}}<g class="overview-line {{.Class}}"><path d="{{.Path}}"/>{{range .Points}}<circle cx="{{printf "%.2f" .X}}" cy="{{printf "%.2f" .Y}}" r="4.5" data-level="{{.Level}}" data-value="{{printf "%.6f" .Value}}"><title>{{.Tooltip}}</title></circle>{{end}}</g>{{end}}</svg>{{else}}<p class="empty-chart">暂无成功执行样本，不绘制测量曲线。</p>{{end}}{{end}}`

const overviewStyle = `
.report-header{padding:4px 0 18px}.report-header h1{font-size:30px;letter-spacing:-.03em;overflow-wrap:anywhere}.section-heading{display:flex;justify-content:space-between;gap:12px;align-items:baseline;flex-wrap:wrap}.section-heading h2{margin:0}.section-kicker{font-size:12px;color:#5c6c7d}.overview{border-top:3px solid #193b55}.overview-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:30px 24px}.overview-panel{min-width:0;padding-top:12px;border-top:1px solid #e4ebf1}.overview-panel h3{font-size:17px;margin:4px 0 12px}.overview-panel h3 small{font-size:11px;font-weight:400;display:block;margin-top:3px}.overview-legend{display:flex;gap:6px 16px;flex-wrap:wrap;min-height:32px;font-size:12px}.overview-key{display:inline-flex;align-items:center;gap:7px;overflow-wrap:anywhere}.overview-key i{height:3px;width:18px;flex:none;background:var(--series-color)}.series-0{--series-color:#087f8c}.series-1{--series-color:#315eb4}.series-2{--series-color:#b96a23}.series-3{--series-color:#855c98}.series-4{--series-color:#587832}.series-5{--series-color:#b74659}.series-6{--series-color:#557688}.series-7{--series-color:#796a36}.overview-plot{display:block;width:100%;height:auto}.overview-axis line{stroke:#dce5ee;stroke-dasharray:3 5}.overview-axis text{fill:#54657a;font:16px ui-monospace,Consolas,monospace}.overview-axis .y-tick{font-size:15px}.overview-line path{stroke:var(--series-color);stroke-width:2.5;fill:none}.overview-line circle{fill:var(--series-color);stroke:#f8fafc;stroke-width:1.2}.overview-line circle:hover{r:6;stroke-width:2}.overview-panel .chart-note{font-size:12px;margin:10px 0 2px;max-width:70ch}.percentile-control{position:relative;display:flex;flex-wrap:wrap;gap:0;align-items:center}.percentile-control>h3{flex:1;min-width:140px}.metric-radio{position:absolute;opacity:0;width:1px;height:1px}.percentile-control>label{display:block;padding:4px 11px;font:12px ui-monospace,Consolas,monospace;border:1px solid #dce5ee;cursor:pointer;margin-bottom:12px}.metric-radio:focus-visible+label{outline:2px solid #1463d9;outline-offset:2px}.metric-radio:checked+label{background:#193b55;color:#f6f8fb;border-color:#193b55}.percentile-views{width:100%}.percentile-view{display:none}#overview-p90:checked~.percentile-views .metric-p90,#overview-p95:checked~.percentile-views .metric-p95,#overview-p99:checked~.percentile-views .metric-p99{display:block}.detail-panel{margin:20px 0}.detail-panel>summary{cursor:pointer;font-size:15px;font-weight:600;padding:14px 0;border-bottom:1px solid #dce5ee}.detail-panel[open]>summary{margin-bottom:16px}.measurement-details{font-size:13px}.report-header .report-nav{margin-top:14px}.detail-panel .load-intro{border-top:1px solid #dce5ee}.summary-alert{padding:12px 16px;background:#fbf4e9;border:1px solid #e6d6ba;border-radius:6px;color:#72521e;font-size:13px;margin:14px 0}
@media(max-width:1050px){.overview-grid{grid-template-columns:minmax(0,1fr);gap:24px}.report-header h1{font-size:26px}.overview-panel h3{font-size:17px}.overview-axis text{font-size:17px}}
`
