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

type matrixCell struct{ Class, Symbol, Tooltip string }
type matrixRow struct {
	ID    string
	Cells []matrixCell
}
type stabilityMatrix struct {
	HasData                    bool
	Columns, MinWidth, BinSize int
	HiddenQueries              int
	Ticks                      []string
	Rows                       []matrixRow
}
type stabilityChart struct {
	ScenarioReport
	DisplayName string
	Matrix      stabilityMatrix
}

func buildStabilityMatrix(s ScenarioReport) stabilityMatrix {
	view := stabilityMatrix{HasData: len(s.Results) > 0}
	if !view.HasData {
		return view
	}
	repeat := s.Repetitions
	for _, result := range s.Results {
		repeat = max(repeat, result.Iteration+1)
	}
	if repeat <= 0 {
		return stabilityMatrix{}
	}
	view.BinSize = max(1, (repeat+59)/60)
	view.Columns = (repeat + view.BinSize - 1) / view.BinSize
	view.MinWidth = max(280, 92+view.Columns*14)
	view.HiddenQueries = max(0, len(s.Stability)-30)
	for column := 0; column < view.Columns; column++ {
		label := ""
		if view.Columns <= 12 || column == 0 || column == view.Columns-1 || (column+1)%5 == 0 {
			label = fmt.Sprint(column*view.BinSize + 1)
		}
		view.Ticks = append(view.Ticks, label)
	}
	type bin struct{ success, assertion, sqlError, records int }
	bins := make(map[string][]bin)
	visible := make(map[string]bool)
	for _, outcome := range s.Stability[:min(30, len(s.Stability))] {
		visible[outcome.QueryID] = true
	}
	for _, result := range s.Results {
		if !visible[result.ID] || result.Iteration < 0 || result.Iteration >= repeat {
			continue
		}
		if bins[result.ID] == nil {
			bins[result.ID] = make([]bin, view.Columns)
		}
		entry := &bins[result.ID][result.Iteration/view.BinSize]
		entry.records++
		if !result.SQLSucceeded {
			entry.sqlError++
		} else {
			entry.success++
			if !result.Pass {
				entry.assertion++
			}
		}
	}
	for _, outcome := range s.Stability[:min(30, len(s.Stability))] {
		row := matrixRow{ID: outcome.QueryID}
		for column := 0; column < view.Columns; column++ {
			var entry bin
			if bins[outcome.QueryID] != nil {
				entry = bins[outcome.QueryID][column]
			}
			first, last := column*view.BinSize+1, min(repeat, (column+1)*view.BinSize)
			missing := max(0, last-first+1-entry.records)
			cell := matrixCell{Class: "same"}
			switch {
			case entry.sqlError > 0:
				cell.Class, cell.Symbol = "sql-error", "×"
			case entry.assertion > 0:
				cell.Class, cell.Symbol = "assertion", "!"
			case missing > 0:
				cell.Class, cell.Symbol = "missing", "·"
			}
			cell.Tooltip = fmt.Sprintf("%s · 第 %d 至 %d 次：成功执行 %d；集合或正确性断言失败 %d；SQL 错误 %d；未执行 %d", outcome.QueryID, first, last, entry.success, entry.assertion, entry.sqlError, missing)
			row.Cells = append(row.Cells, cell)
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

const matrixHTML = `{{define "stabilityMatrix"}}{{if .HasData}}<div class="matrix-legend"><span><i class="same"></i>与基准相同，断言通过</span><span><i class="assertion">!</i>集合变化 / 断言失败</span><span><i class="sql-error">×</i>SQL 错误</span><span><i class="missing">·</i>未执行</span></div><p class="matrix-caption">{{if eq .BinSize 1}}每格为一次执行{{else}}每格最多 {{.BinSize}} 次执行，异常优先着色{{end}}，悬停查看次数和错误计数；可左右滑动查看全部轮次。</p><div class="heatmap-scroll"><div class="heatmap" style="--columns:{{.Columns}};--matrix-width:{{.MinWidth}}px"><div class="heatmap-axis"><span>查询 / 轮次</span>{{range .Ticks}}<span>{{.}}</span>{{end}}</div>{{range .Rows}}<div class="heatmap-row"><strong>{{.ID}}</strong>{{range .Cells}}<span class="matrix-cell {{.Class}}" role="img" aria-label="{{.Tooltip}}" title="{{.Tooltip}}">{{.Symbol}}</span>{{end}}</div>{{end}}</div></div>{{if .HiddenQueries}}<p class="matrix-caption">图中展示前 30 条查询，其余 {{.HiddenQueries}} 条见详细数值或原始记录。</p>{{end}}{{else}}<p class="empty-chart">未执行重复结果比较，暂无稳定性图。</p>{{end}}{{end}}`

const matrixStyle = `
.stability-section>.section-heading{padding:16px 0 4px}.stability-chart h3{margin:0 0 5px;font-size:18px}.stability-chart .chart-note{margin:8px 0 14px}.matrix-legend{display:flex;flex-wrap:wrap;gap:8px 18px;font-size:12px;margin:16px 0 6px}.matrix-legend span{display:inline-flex;align-items:center;gap:7px}.matrix-legend i{display:inline-flex;width:13px;height:13px;border-radius:3px;font:12px/13px ui-monospace,Consolas,monospace;justify-content:center;align-items:center;font-style:normal}.same{background:#8bc2af;color:#164d3c}.assertion{background:#f0c174;color:#694816}.sql-error{background:#dc8f89;color:#6a251f}.missing{background:#dce3eb;color:#5c6c7d}.matrix-caption{font-size:12px;color:#5c6c7d;margin:8px 0 10px}.heatmap-scroll{overflow-x:auto;padding:5px 0 12px}.heatmap{min-width:var(--matrix-width)}.heatmap-axis,.heatmap-row{display:grid;grid-template-columns:92px repeat(var(--columns),minmax(10px,1fr));gap:4px;align-items:center;margin:5px 0}.heatmap-axis{font:11px ui-monospace,Consolas,monospace;color:#5c6c7d;text-align:center}.heatmap-axis>span:first-child{text-align:left}.heatmap-row>strong{position:sticky;left:0;background:#fff;font:12px ui-monospace,Consolas,monospace;overflow-wrap:anywhere;z-index:1}.matrix-cell{display:flex;align-items:center;justify-content:center;height:22px;border-radius:3px;font:14px ui-monospace,Consolas,monospace;cursor:help}.matrix-cell:hover{outline:2px solid #193b55;outline-offset:1px}.stability-chart>details{margin-top:16px}.stability-chart>details>summary{cursor:pointer;font-size:13px;color:#1463d9}.case-id{font-size:12px;color:#5c6c7d}
`
