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
)

type latencyBar struct {
	Label string
	Value float64
	Width float64
	Y     int
}

type latencyRow struct {
	ID          string
	Concurrency int
	SessionSQL  []string
	Samples     int
	Bars        []latencyBar
}

type latencyTick struct {
	X     float64
	Value float64
}

type reportView struct {
	Report
	LatencyRows              []latencyRow
	LatencyTicks             []latencyTick
	RendererVersion          string
	ConcurrencySeries        []concurrencySeries
	StabilityScenarios       []ScenarioReport
	HasLatencySamples        bool
	Overview                 chartOverview
	StabilityCharts          []stabilityChart
	QueryQualities           []queryQualityChart
	DisplayConcurrencyLevels []int
	DisplayScenarioIDs       []string
	HiddenProfiles           int
	Checks                   checkSummary
	RetrievalQuality         []retrievalQualityRow
	EnvironmentView          environmentPresentation
	ResourcePlots            []resourcePlot
}

type reportSelection struct {
	Levels      []int
	ScenarioIDs []string
}

// Reconstruct percentiles from observations so reports written before p90_ms
// was added can be displayed without modifying their raw measurement files.
func makeReportView(report Report, levels ...int) (reportView, error) {
	return makeSelectedReportView(report, reportSelection{Levels: levels})
}

func makeSelectedReportView(report Report, selection reportSelection) (reportView, error) {
	levels := selection.Levels
	view := reportView{Report: report, RendererVersion: version}
	view.EnvironmentView = buildEnvironmentPresentation(report)
	view.ResourcePlots = buildResourcePlots(report.Environment)
	view.Checks = summarizeChecks(report)
	view.Scenarios = make([]ScenarioReport, 0, len(report.Scenarios))
	selected, present := make(map[int]bool), make(map[int]bool)
	if len(levels) > 8 {
		return reportView{}, fmt.Errorf("at most 8 display concurrency levels are supported")
	}
	for _, level := range levels {
		if level < 1 || level > 128 || selected[level] {
			return reportView{}, fmt.Errorf("display concurrency levels must be unique integers from 1 to 128")
		}
		selected[level] = true
	}
	view.DisplayConcurrencyLevels = append([]int(nil), levels...)
	selectedIDs, presentIDs := make(map[string]bool), make(map[string]bool)
	for _, id := range selection.ScenarioIDs {
		if !safeIdentifier.MatchString(id) || selectedIDs[id] {
			return reportView{}, fmt.Errorf("display scenario IDs must be unique safe identifiers")
		}
		selectedIDs[id] = true
	}
	view.DisplayScenarioIDs = append([]string(nil), selection.ScenarioIDs...)
	for _, s := range report.Scenarios {
		level := s.EffectiveConcurrency
		if level == 0 {
			level = report.Profile.Concurrency
		}
		if s.Oracle != "stable_multiset" {
			if len(levels) > 0 && !selected[level] || len(selectedIDs) > 0 && !selectedIDs[s.ID] {
				view.HiddenProfiles++
				continue
			}
			present[level] = true
			presentIDs[s.ID] = true
		}
		view.Scenarios = append(view.Scenarios, s)
	}
	for _, id := range selection.ScenarioIDs {
		if !presentIDs[id] {
			return reportView{}, fmt.Errorf("report has no ordinary scenario matching the requested selection: %s", id)
		}
	}
	for _, level := range levels {
		if !present[level] {
			return reportView{}, fmt.Errorf("report has no ordinary scenario at requested concurrency %d", level)
		}
	}
	var maximum float64
	for i := range view.Scenarios {
		s := &view.Scenarios[i]
		if s.EffectiveConcurrency == 0 {
			s.EffectiveConcurrency = report.Profile.Concurrency
		}
		s.Stability = append([]StabilityResult(nil), s.Stability...)
		counts := make(map[string]StabilityResult)
		s.SQLFailures, s.AssertionFailures, s.SQLSuccesses = 0, 0, 0
		s.MeanScore = 0
		latencies := make([]float64, 0, len(s.Results))
		for _, result := range s.Results {
			count := counts[result.ID]
			count.Executions++
			if result.SQLSucceeded {
				s.SQLSuccesses++
				count.SQLSuccesses++
				if !result.Pass {
					s.AssertionFailures++
				}
			} else {
				s.SQLFailures++
			}
			if !result.Pass {
				count.Failures++
			}
			counts[result.ID] = count
			if !result.SQLSucceeded {
				continue
			}
			if result.LatencyMS < 0 || math.IsNaN(result.LatencyMS) || math.IsInf(result.LatencyMS, 0) {
				return reportView{}, fmt.Errorf("scenario %s has an invalid SQL latency", s.ID)
			}
			if result.Score < 0 || result.Score > 1 || math.IsNaN(result.Score) || math.IsInf(result.Score, 0) {
				return reportView{}, fmt.Errorf("scenario %s has an invalid quality score", s.ID)
			}
			s.MeanScore += result.Score
			latencies = append(latencies, result.LatencyMS)
		}
		row := latencyRow{ID: s.ID, Concurrency: s.EffectiveConcurrency, SessionSQL: s.SessionSQL, Samples: len(latencies)}
		if len(latencies) > 0 {
			s.MeanScore /= float64(s.SQLSuccesses)
			view.HasLatencySamples = true
			setLatencyPercentiles(s, latencies)
			row.Bars = []latencyBar{{Label: "P90", Value: s.P90MS, Y: 5}, {Label: "P95", Value: s.P95MS, Y: 35}, {Label: "P99", Value: s.P99MS, Y: 65}}
			maximum = math.Max(maximum, s.P99MS)
		}
		view.LatencyRows = append(view.LatencyRows, row)
		if s.Oracle == "stable_multiset" {
			for i := range s.Stability {
				count := counts[s.Stability[i].QueryID]
				s.Stability[i].Executions = count.Executions
				s.Stability[i].SQLSuccesses = count.SQLSuccesses
				s.Stability[i].Failures = count.Failures
			}
			view.StabilityScenarios = append(view.StabilityScenarios, *s)
			view.StabilityCharts = append(view.StabilityCharts, stabilityChart{ScenarioReport: *s, DisplayName: overviewLabel(concurrencySeries{ID: s.ID, SessionSQL: s.SessionSQL}), Matrix: buildStabilityMatrix(*s)})
		}
	}
	// One shared zero-based axis prevents independently scaled bars from hiding
	// the difference between scenarios. Leave space for the value labels.
	axisMaximum := maximum
	if axisMaximum == 0 {
		axisMaximum = 1
	}
	for i := range view.LatencyRows {
		for j := range view.LatencyRows[i].Bars {
			bar := &view.LatencyRows[i].Bars[j]
			bar.Width = bar.Value / axisMaximum * 640
		}
	}
	for i := 0; i <= 4; i++ {
		view.LatencyTicks = append(view.LatencyTicks, latencyTick{X: float64(i) * 160, Value: axisMaximum * (float64(i) / 4)})
	}
	if len(report.Profile.ConcurrencyLevels) > 0 || len(levels) > 0 {
		var err error
		view.ConcurrencySeries, err = buildConcurrencySeries(view.Scenarios)
		if err != nil {
			return reportView{}, err
		}
		view.Overview = buildChartOverview(view.ConcurrencySeries)
	}
	if !view.Overview.Enabled {
		for _, s := range view.Scenarios {
			if s.Oracle != "stable_multiset" {
				view.QueryQualities = append(view.QueryQualities, buildQueryQualityChart(s))
			}
		}
	}
	view.RetrievalQuality = buildRetrievalQualityRows(view.Scenarios)
	return view, nil
}

const latencyHTML = `
<section class="card latency" id="latency">
  <div class="chart-heading"><div><h2>查询延迟分位数</h2><p class="lead">P90 · P95 · P99 / 毫秒 · 越短越快</p></div>
    <div class="chart-legend" aria-label="图例"><span><i class="p90"></i>P90</span><span><i class="p95"></i>P95</span><span><i class="p99"></i>P99</span></div>
  </div>
  <p class="chart-note">P95 表示本次样本中至少 95% 的成功 SQL 查询耗时不超过该值。各场景共用线性时间轴，标签保留实际数值。</p>
  {{range .LatencyRows}}
  <div class="latency-row">
    <div class="scenario-label"><strong>{{scenarioName .ID}}</strong>{{range .SessionSQL}}<code>{{.}}</code>{{end}}<small>{{if gt .Concurrency 0}}客户端并发上限 {{.Concurrency}} · {{end}}成功 SQL 样本 {{.Samples}}{{if and (gt .Samples 0) (lt .Samples 100)}} · P99 为样本最大值{{end}}</small></div>
    {{if .Bars}}
    <div class="chart-scroll"><svg class="latency-bars" viewBox="0 0 800 98" role="img" aria-label="{{scenarioName .ID}} 的 P90、P95、P99 延迟">
      {{range $.LatencyTicks}}<line x1="{{printf "%.2f" .X}}" x2="{{printf "%.2f" .X}}" y1="0" y2="92" class="chart-grid"/>{{end}}
      {{range .Bars}}<g transform="translate(0 {{.Y}})" class="bar-{{.Label}}"><title>{{.Label}}：{{printf "%.2f" .Value}} ms</title><rect width="{{printf "%.2f" .Width}}" height="19" rx="3"/><text x="{{printf "%.2f" .Width}}" dx="10" y="14">{{.Label}} {{printf "%.2f" .Value}} ms</text></g>{{end}}
    </svg></div>
    {{else}}<p class="empty-chart">没有成功 SQL 样本，暂无延迟分位数。</p>{{end}}
  </div>
  {{else}}<p class="empty-chart">未执行查询场景，暂无延迟数据。</p>
  {{end}}
  {{if .HasLatencySamples}}<div class="latency-axis"><div></div><div class="chart-scroll"><svg viewBox="0 0 800 26" role="img" aria-label="共享时间轴，单位毫秒">{{range .LatencyTicks}}<text x="{{printf "%.2f" .X}}" y="17" {{if gt .X 0.0}}text-anchor="middle"{{end}}>{{printf "%.1f" .Value}}</text>{{end}}<text x="800" y="17" text-anchor="end">ms</text></svg></div></div>{{end}}
  <p class="chart-note">统计所有成功 SQL 的实测延迟，排除 SQL 错误和预热。使用最近秩法 ceil(p × 样本数)。少于 100 条样本时，P99 等于样本最大耗时。缓存、并发和运行顺序会影响跨场景的延迟比较。</p>
</section>`

const latencyStyle = `
.latency{border-top:3px solid #193b55}.chart-heading{display:flex;justify-content:space-between;align-items:center;gap:20px;flex-wrap:wrap}.chart-heading h2{margin:0 0 3px}.chart-heading p{margin:0}.chart-legend{display:flex;gap:20px;font:13px ui-monospace,"Cascadia Code",Consolas,monospace}.chart-legend span{display:flex;align-items:center;gap:7px}.chart-legend i{width:10px;height:10px;border-radius:2px}.p90,.bar-P90 rect{background:#087f8c;fill:#087f8c}.p95,.bar-P95 rect{background:#315eb4;fill:#315eb4}.p99,.bar-P99 rect{background:#b96a23;fill:#b96a23}.chart-note{color:#5c6c7d;font-size:13px;max-width:900px;margin:16px 0}.latency-row,.latency-axis{display:grid;grid-template-columns:210px minmax(0,1fr);gap:24px;align-items:center}.latency-row{padding:18px 0;border-bottom:1px solid #e4ebf1}.scenario-label{display:flex;flex-direction:column;gap:6px;min-width:0;overflow-wrap:anywhere}.scenario-label strong{font-size:14px}.scenario-label code{font-size:12px;padding:0;background:transparent}.scenario-label small{font-size:12px}.chart-scroll{overflow-x:auto;min-width:0}.latency svg{display:block;width:100%;min-width:600px;overflow:visible}.latency svg text{font:12px ui-monospace,"Cascadia Code",Consolas,monospace;fill:#2c4154;font-variant-numeric:tabular-nums}.chart-grid{stroke:#e4ebf1;stroke-dasharray:3 4}.latency-axis{padding:9px 0}.empty-chart{color:#5c6c7d;font-size:13px}.latency-row g:hover rect{opacity:.8}
@media(max-width:760px){.latency-row,.latency-axis{grid-template-columns:minmax(0,1fr);gap:12px}.latency-axis>div:first-child{display:none}.chart-heading{gap:12px}.chart-legend{gap:16px}}
`
