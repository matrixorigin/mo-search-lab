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

type concurrencyPoint struct {
	ScenarioReport
	X, QPSY, P90Y, P95Y, P99Y float64
	HasSamples                bool
}

type concurrencySeries struct {
	ID                                 string
	Route                              string
	SessionSQL                         []string
	Oracle                             string
	Points                             []concurrencyPoint
	QPSMaximum, LatencyMaximum         float64
	QPSPath, P90Path, P95Path, P99Path string
	HasSamples                         bool
	ColorIndex                         int
}

func buildConcurrencySeries(scenarios []ScenarioReport) ([]concurrencySeries, error) {
	var series []concurrencySeries
	indices := make(map[string]int)
	for _, s := range scenarios {
		if s.Oracle == "stable_multiset" {
			continue
		}
		if s.EffectiveConcurrency < 1 || s.QPS < 0 || math.IsNaN(s.QPS) || math.IsInf(s.QPS, 0) {
			return nil, fmt.Errorf("scenario %s has invalid concurrency or QPS", s.ID)
		}
		index, ok := indices[s.ID]
		if !ok {
			index = len(series)
			indices[s.ID] = index
			series = append(series, concurrencySeries{ID: s.ID, Route: s.Route, Oracle: s.Oracle, SessionSQL: s.SessionSQL, ColorIndex: index})
		}
		group := &series[index]
		for _, p := range group.Points {
			if p.EffectiveConcurrency == s.EffectiveConcurrency || p.ScenarioSHA256 != s.ScenarioSHA256 || p.QueriesSHA256 != s.QueriesSHA256 {
				return nil, fmt.Errorf("scenario %s has duplicate profiles or different scenario/query identities", s.ID)
			}
		}
		group.Points = append(group.Points, concurrencyPoint{ScenarioReport: s, HasSamples: s.SQLSuccesses > 0})
		if s.SQLSuccesses > 0 {
			group.HasSamples = true
			group.QPSMaximum = math.Max(group.QPSMaximum, s.QPS)
			group.LatencyMaximum = math.Max(group.LatencyMaximum, s.P99MS)
		}
	}
	for i := range series {
		group := &series[i]
		if group.QPSMaximum == 0 {
			group.QPSMaximum = 1
		}
		if group.LatencyMaximum == 0 {
			group.LatencyMaximum = 1
		}
		sort.Slice(group.Points, func(i, j int) bool {
			return group.Points[i].EffectiveConcurrency < group.Points[j].EffectiveConcurrency
		})
		var qps, p90, p95, p99 strings.Builder
		pen := "M"
		for j := range group.Points {
			p := &group.Points[j]
			p.X = 238
			if len(group.Points) > 1 {
				p.X = 64 + float64(j)/float64(len(group.Points)-1)*348
			}
			if !p.HasSamples {
				pen = "M"
				continue
			}
			p.QPSY = 156 - p.QPS/group.QPSMaximum*130
			p.P90Y = 156 - p.P90MS/group.LatencyMaximum*130
			p.P95Y = 156 - p.P95MS/group.LatencyMaximum*130
			p.P99Y = 156 - p.P99MS/group.LatencyMaximum*130
			fmt.Fprintf(&qps, "%s%.2f %.2f ", pen, p.X, p.QPSY)
			fmt.Fprintf(&p90, "%s%.2f %.2f ", pen, p.X, p.P90Y)
			fmt.Fprintf(&p95, "%s%.2f %.2f ", pen, p.X, p.P95Y)
			fmt.Fprintf(&p99, "%s%.2f %.2f ", pen, p.X, p.P99Y)
			pen = "L"
		}
		group.QPSPath, group.P90Path, group.P95Path, group.P99Path = qps.String(), p90.String(), p95.String(), p99.String()
	}
	return series, nil
}

const profileHTML = `<p>查询上限 {{.Profile.QueryLimit}} · 普通查询重复 {{.Profile.Repeat}} · {{if .DisplayConcurrencyLevels}}报告展示并发档位 {{range $i, $c := .DisplayConcurrencyLevels}}{{if $i}} / {{end}}{{$c}}{{end}}{{else if .Profile.ConcurrencyLevels}}客户端并发档位 {{range $i, $c := .Profile.ConcurrencyLevels}}{{if $i}} / {{end}}{{$c}}{{end}}{{else}}客户端并发上限 {{.Profile.Concurrency}}{{end}} · 预热 {{.Profile.Warmup}} · 超时 {{.Profile.Timeout}}</p>
<p>{{if .Profile.SQLAddress}}普通查询入口：<code>{{.Profile.SQLAddress}}</code><br>{{end}}稳定性 SQL 入口：{{range $i, $e := .Profile.QueryEndpoints}}{{if $i}}、{{end}}<code>{{$e}}</code>{{end}}{{if .Profile.StabilityRepeat}} · 稳定性重复 {{.Profile.StabilityRepeat}}{{end}}{{if .Profile.StabilityQueryLimit}} · 稳定性查询上限 {{.Profile.StabilityQueryLimit}}{{end}}</p>
<p class="report-nav">{{if .ConcurrencySeries}}<a href="#concurrency">并发表现</a>{{end}}{{if .StabilityScenarios}}<a href="#stability">结果稳定性</a>{{end}}<a href="#latency">延迟分位数</a><a href="report.json">原始记录</a></p>`

const concurrencyHTML = `{{if .ConcurrencySeries}}<section id="concurrency"><section class="card load-intro"><h2>并发表现</h2><p class="lead">固定数据与索引，比较客户端并发档位下的吞吐和延迟。</p><p class="chart-note">QPS = 成功查询执行数 / 测量秒数，包含质量未过线的查询。单 SQL 场景统计 SQL 执行；hybrid_rrf 场景统计两路 SQL 均成功的一次融合请求。{{if .Profile.MixedScenarios}}_mixed 场景分别统计各路成功 SQL，使用同一批次时长；每对作业等待两路完成。{{end}}各档位依次执行，分别预热；有限查询批次与缓存状态会影响结果。这是本次负载下的观察值。</p><p class="chart-note">查询入口数量不能证明 CN 数量。稳定性场景按并发 1 单独执行；实际端点和物理计划见稳定性详情。</p></section>
{{range .ConcurrencySeries}}<section class="card concurrency-series"><h3>{{.ID}}</h3>{{range .SessionSQL}}<p><code>{{.}}</code></p>{{end}}
<p class="mobile-scroll-note">图表和表格可左右滑动查看。</p>{{if .HasSamples}}<div class="load-chart-grid">
  <div class="load-chart"><h4>{{if eq .Route "hybrid_rrf"}}成功融合请求{{else}}成功 SQL{{end}}吞吐 / QPS</h4><div class="chart-legend"><span><i class="qps"></i>{{if eq .Route "hybrid_rrf"}}成功融合请求{{else}}成功 SQL{{end}} / 秒</span></div><div class="chart-scroll"><svg viewBox="0 0 440 202" role="img" aria-label="{{.ID}} 的并发与成功查询 QPS">
    <line x1="64" x2="412" y1="156" y2="156" class="chart-grid"/><line x1="64" x2="412" y1="26" y2="26" class="chart-grid"/>
    <text x="56" y="160" text-anchor="end">0</text><text x="56" y="30" text-anchor="end">{{printf "%.1f" .QPSMaximum}}</text>
    <path d="{{.QPSPath}}" class="load-qps"/>
    {{range .Points}}{{if .HasSamples}}<circle cx="{{printf "%.2f" .X}}" cy="{{printf "%.2f" .QPSY}}" r="4" class="load-qps-dot"><title>并发 {{.EffectiveConcurrency}}：{{printf "%.2f" .QPS}} QPS，成功查询 {{.SQLSuccesses}} 次</title></circle>{{end}}<text x="{{printf "%.2f" .X}}" y="179" text-anchor="middle">{{.EffectiveConcurrency}}</text>{{end}}
    <text x="238" y="199" text-anchor="middle">客户端并发档位</text>
  </svg></div></div>
  <div class="load-chart"><h4>查询延迟 / ms</h4><div class="chart-legend"><span><i class="p90"></i>P90</span><span><i class="p95"></i>P95</span><span><i class="p99"></i>P99</span></div><div class="chart-scroll"><svg viewBox="0 0 440 202" role="img" aria-label="{{.ID}} 的并发与 P90 P95 P99 延迟">
    <line x1="64" x2="412" y1="156" y2="156" class="chart-grid"/><line x1="64" x2="412" y1="26" y2="26" class="chart-grid"/>
    <text x="56" y="160" text-anchor="end">0</text><text x="56" y="30" text-anchor="end">{{printf "%.1f" .LatencyMaximum}}</text>
    <path d="{{.P90Path}}" class="line-p90"/><path d="{{.P95Path}}" class="line-p95"/><path d="{{.P99Path}}" class="line-p99"/>
    {{range .Points}}{{if .HasSamples}}<circle cx="{{printf "%.2f" .X}}" cy="{{printf "%.2f" .P90Y}}" r="3" class="dot-p90"><title>并发 {{.EffectiveConcurrency}}：P90 {{printf "%.2f" .P90MS}} ms</title></circle><circle cx="{{printf "%.2f" .X}}" cy="{{printf "%.2f" .P95Y}}" r="3" class="dot-p95"><title>并发 {{.EffectiveConcurrency}}：P95 {{printf "%.2f" .P95MS}} ms</title></circle><circle cx="{{printf "%.2f" .X}}" cy="{{printf "%.2f" .P99Y}}" r="3" class="dot-p99"><title>并发 {{.EffectiveConcurrency}}：P99 {{printf "%.2f" .P99MS}} ms</title></circle>{{end}}<text x="{{printf "%.2f" .X}}" y="179" text-anchor="middle">{{.EffectiveConcurrency}}</text>{{end}}
    <text x="238" y="199" text-anchor="middle">客户端并发档位</text>
  </svg></div></div>
</div>{{else}}<p class="empty-chart">此场景尚无成功 SQL 样本，暂无并发曲线。</p>{{end}}
<div class="chart-scroll"><table class="load-table"><tr><th>客户端并发上限</th><th>查询 / 执行</th><th>{{if eq .Route "hybrid_rrf"}}成功融合请求{{else}}成功 SQL{{end}}</th><th>测量秒数</th><th>QPS</th><th>P90 / P95 / P99 ms</th><th>{{if eq .Oracle "ann_recall"}}平均 Recall@{{(index .Points 0).TopK}}{{else}}质量均值{{end}}</th><th>执行错误</th><th>断言失败</th></tr>
{{range .Points}}<tr><td>{{.EffectiveConcurrency}}</td><td>{{.SelectedQueries}} / {{.Executions}}</td><td>{{.SQLSuccesses}}</td><td>{{printf "%.3f" .MeasuredSeconds}}</td>{{if .HasSamples}}<td>{{printf "%.2f" .QPS}}</td><td>{{printf "%.2f / %.2f / %.2f" .P90MS .P95MS .P99MS}}</td><td>{{printf "%.3f" .MeanScore}}</td>{{else}}<td colspan="3">无成功 SQL 样本</td>{{end}}<td>{{.SQLFailures}}</td><td>{{.AssertionFailures}}{{if .Error}}<br><span class="fail">{{.Error}}</span>{{end}}</td></tr>{{end}}
</table></div>

</section>{{end}}</section>{{end}}`

const loadStyle = `
html{scroll-behavior:smooth}.report-nav{display:flex;flex-wrap:wrap;gap:10px;margin-top:20px}.report-nav a{padding:7px 12px;border:1px solid #dce5ee;border-radius:6px;text-decoration:none;font-size:13px}.report-nav a:hover{background:#edf4fb}.load-intro{border-top:3px solid #193b55}.concurrency-series h3{margin:0;font-size:18px}.load-chart-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:24px;margin:24px 0}.load-chart h4{font-size:14px;margin:0 0 8px}.load-chart{min-width:0}.load-chart .chart-legend{font-size:11px;margin-bottom:8px}.load-chart svg{display:block;width:100%;min-width:400px}.load-chart svg text{font:12px ui-monospace,Consolas,monospace;fill:#54657a}.load-chart path{stroke-width:2.5;fill:none}.chart-legend .qps{background:#193b55}.load-qps{stroke:#193b55}.load-qps-dot{fill:#193b55}.line-p90{stroke:#087f8c}.line-p95{stroke:#315eb4}.line-p99{stroke:#b96a23}.dot-p90{fill:#087f8c}.dot-p95{fill:#315eb4}.dot-p99{fill:#b96a23}.load-table{font-size:13px;white-space:nowrap}.stability-detail{margin:14px 0}.stability-detail summary{cursor:pointer;color:#1463d9}.stability-detail pre{font-size:12px;max-height:280px;overflow:auto}.stability-table{font-size:13px;white-space:nowrap}.mobile-scroll-note{display:none}
@media(max-width:760px){.load-chart-grid{grid-template-columns:minmax(0,1fr)}.mobile-scroll-note{display:block;font-size:12px;color:#5c6c7d}}
@media(prefers-reduced-motion:reduce){html{scroll-behavior:auto}}
`
