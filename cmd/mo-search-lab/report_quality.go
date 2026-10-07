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

import "fmt"

type queryQualityBar struct {
	ID, Class, Tooltip  string
	Index               int
	X, Y, Width, Height float64
	HasSample           bool
}

type queryQualityChart struct {
	ID, Title     string
	HasData       bool
	HiddenQueries int
	ThresholdY    float64
	Threshold     float64
	Bars          []queryQualityBar
}

func buildQueryQualityChart(s ScenarioReport) queryQualityChart {
	chart := queryQualityChart{ID: s.ID, Title: "单查询断言结果"}
	if s.Oracle == "ann_recall" {
		chart.Title = fmt.Sprintf("单查询 Recall@%d", s.TopK)
	} else if s.Oracle == "qrels" {
		chart.Title = fmt.Sprintf("单查询 nDCG@%d", s.TopK)
	}
	type outcome struct {
		score                         float64
		successes, assertions, errors int
	}
	byID := make(map[string]*outcome)
	var ids []string
	for _, result := range s.Results {
		entry := byID[result.ID]
		if entry == nil {
			entry = &outcome{}
			byID[result.ID] = entry
			ids = append(ids, result.ID)
		}
		if !result.SQLSucceeded {
			entry.errors++
		} else {
			entry.successes++
			entry.score += result.Score
			if !result.Pass {
				entry.assertions++
			}
		}
	}
	chart.HiddenQueries = max(0, len(ids)-30)
	ids = ids[:min(30, len(ids))]
	for i, id := range ids {
		entry := byID[id]
		value := 0.0
		if entry.successes > 0 {
			value = entry.score / float64(entry.successes)
			chart.HasData = true
		}
		class := "quality-pass"
		if entry.assertions > 0 {
			class = "quality-fail"
		}
		if entry.errors > 0 || entry.successes == 0 {
			class = "quality-incomplete"
		}
		step := 620.0 / float64(len(ids))
		tooltip := fmt.Sprintf("%s · 无成功执行样本；SQL 错误 %d", id, entry.errors)
		if entry.successes > 0 {
			tooltip = fmt.Sprintf("%s · %s %.3f；成功执行 %d；断言失败 %d；SQL 错误 %d", id, chart.Title, value, entry.successes, entry.assertions, entry.errors)
		}
		chart.Bars = append(chart.Bars, queryQualityBar{ID: id, Index: i + 1, Class: class, X: 64 + float64(i)*step + step*.15, Y: 218 - value*194, Width: step * .7, Height: value * 194, HasSample: entry.successes > 0,
			Tooltip: tooltip})
	}
	chart.Threshold, chart.ThresholdY = s.MinScore, 218-s.MinScore*194
	return chart
}

const queryQualityHTML = `<!-- quality:start -->{{if .QueryQualities}}<section id="quality"><div class="section-heading"><h2>单查询质量</h2><span class="section-kicker">原始查询记录 · 0–1</span></div>{{range .QueryQualities}}<article class="card query-quality"><h3>{{.Title}}</h3><p class="case-id">场景：{{.ID}}</p><div class="matrix-legend"><span><i class="same"></i>全部断言通过</span><span><i class="assertion"></i>有断言未通过</span><span><i class="missing"></i>有执行错误 / 不完整</span></div>{{if .HasData}}<div class="chart-scroll"><svg class="query-quality-plot" viewBox="0 0 720 276" role="img" aria-label="{{.ID}} 的逐查询质量评分"><g class="overview-axis"><line x1="64" x2="684" y1="218" y2="218"/><line x1="64" x2="684" y1="24" y2="24"/><text x="54" y="223" text-anchor="end">0</text><text x="54" y="29" text-anchor="end">1</text><text x="374" y="267" text-anchor="middle">选定查询序号</text></g>{{if .Threshold}}<line x1="64" x2="684" y1="{{printf "%.2f" .ThresholdY}}" y2="{{printf "%.2f" .ThresholdY}}" class="quality-threshold"/><text x="684" y="{{printf "%.2f" .ThresholdY}}" dy="-7" text-anchor="end" class="threshold-label">单查询阈值 {{printf "%.2f" .Threshold}}</text>{{end}}{{range .Bars}}<g class="{{.Class}}"><title>{{.Tooltip}}</title><rect x="{{printf "%.2f" .X}}" y="24" width="{{printf "%.2f" .Width}}" height="194" class="quality-track"/>{{if .HasSample}}<rect x="{{printf "%.2f" .X}}" y="{{printf "%.2f" .Y}}" width="{{printf "%.2f" .Width}}" height="{{printf "%.2f" .Height}}" class="quality-value"/>{{if eq .Height 0.0}}<line x1="{{printf "%.2f" .X}}" x2="{{printf "%.2f" .X}}" y1="215" y2="221" class="quality-zero"/>{{end}}{{else}}<text x="{{printf "%.2f" .X}}" y="125">×</text>{{end}}<text x="{{printf "%.2f" .X}}" y="240" class="query-index">{{.Index}}</text></g>{{end}}</svg></div>{{else}}<p class="empty-chart">当前展示的查询没有成功执行样本，不绘制质量评分。</p>{{end}}<p class="chart-note">每柱一条查询，多次成功执行取评分均值；悬停查看查询 ID 与错误数；窄屏可左右滑动查看全部查询。{{if .HiddenQueries}}展示前 30 条，其余 {{.HiddenQueries}} 条保留在原始记录中。{{end}}</p></article>{{end}}</section>{{end}}<!-- quality:end -->`

const queryQualityStyle = `.query-quality h3{margin:0;font-size:18px}.query-quality-plot{width:100%;min-width:540px;display:block}.quality-track{fill:#f0f4f7}.quality-pass .quality-value{fill:#4e9e80}.quality-fail .quality-value{fill:#cc963d}.quality-incomplete .quality-value{fill:#8393a3}.quality-zero{stroke:#cc963d;stroke-width:4}.query-index,.threshold-label{font:12px ui-monospace,Consolas,monospace;fill:#5c6c7d}.quality-threshold{stroke:#a87326;stroke-width:1.5;stroke-dasharray:5 4}`
