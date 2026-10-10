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
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ensureReportTarget(dir string) error {
	for _, name := range []string{"report.json", "report.html", "environment.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return fmt.Errorf("report already exists: %s", filepath.Join(dir, name))
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

const reportHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>MO Search Lab · 检索现场报告</title><style>
body{font:15px/1.55 system-ui,sans-serif;color:#172335;background:#f3f6fa;margin:0}main{max-width:1100px;margin:auto;padding:32px 20px}h1{margin-bottom:4px}.lead{color:#54657a}.card{background:#fff;border:1px solid #dce5ee;border-radius:12px;padding:20px;margin:18px 0;overflow:auto}table{border-collapse:collapse;width:100%}th,td{padding:10px;text-align:left;border-bottom:1px solid #dce5ee}th{background:#edf4fb}.pass{color:#087348}.fail{color:#b42318}code,pre{white-space:pre-wrap;word-break:break-all;background:#f2f5f8;padding:3px}small{color:#5c6c7d}a{color:#1463d9}
<!-- chart-style --></style></head><body><main><header class="report-header"><h1>MO Search Lab · {{.DisplayName}} 检索报告</h1><p class="lead">{{.StartedAt.Format "2006-01-02 15:04:05 UTC"}} · MO {{.MatrixOneVersion}} · <strong class="{{if eq .Status "passed"}}pass{{else}}fail{{end}}">{{if eq .MeasurementMode "observe"}}{{if eq .Status "passed"}}测量完成{{else}}运行有错误{{end}}{{else}}{{if eq .Status "passed"}}本次断言通过{{else}}存在未通过检查{{end}}{{end}}</strong></p><p class="report-nav">{{if .Overview.Enabled}}<a href="#overview">并发 / 延迟 / 质量</a>{{end}}{{if .StabilityCharts}}<a href="#stability">重复稳定性</a>{{end}}<a href="#query-details">详细数值</a><a href="report.json">原始记录</a></p><small>{{if eq .MeasurementMode "observe"}}展示本次实测性能、检索质量和重复结果变化，不设验收阈值。{{else}}图中数值为本次负载观察；断言通过不表示达到统一性能验收线。{{end}}</small>{{if .HiddenProfiles}}<p class="chart-note">{{if .DisplayScenarioIDs}}本页仅展示选定场景 {{range $i, $s := .DisplayScenarioIDs}}{{if $i}} / {{end}}{{$s}}{{end}} 的已有实测及独立稳定性检查。{{if .DisplayConcurrencyLevels}} 展示客户端并发 {{range $i, $c := .DisplayConcurrencyLevels}}{{if $i}} / {{end}}{{$c}}{{end}}。{{end}}{{else}}本页仅展示客户端并发 {{range $i, $c := .DisplayConcurrencyLevels}}{{if $i}} / {{end}}{{$c}}{{end}} 的已有实测及独立稳定性检查。{{end}}整体判定沿用原始运行；完整测量与错误记录保存在 <a href="report.json">原始 JSON</a>。</p>{{end}}</header>
<!-- report-tabs -->
<section id="report-performance" role="tabpanel" aria-labelledby="tab-performance" data-report-panel>
<!-- overview -->
{{if .Profile.MixedScenarios}}<p class="chart-note">两路 SQL 同时运行：{{range $i, $id := .Profile.MixedScenarios}}{{if $i}} + {{end}}{{$id}}{{end}}。普通场景独立运行，_mixed 场景同时运行。并发 C 表示 C 个作业，每个作业同时发送两条 SQL 并等待两路完成，最多同时执行 2C 条 SQL；各路延迟独立记录，QPS 共用批次时长。质量计算在计时后进行，未执行客户端融合。</p>{{end}}
<!-- retrieval-quality -->
<!-- quality -->
<!-- stability -->
{{if .ConcurrencySeries}}<details class="detail-panel"><summary>各场景完整并发图表与数值</summary><!-- concurrency --></details>{{end}}
{{if .Overview.Enabled}}<details class="detail-panel"><summary>全部场景 P50 / P90 / P95 / P99</summary><!-- latency --></details>{{else}}<!-- latency -->{{end}}
<details class="detail-panel" id="query-details"><summary>逐场景数值与失败计数 · {{len .Scenarios}} 组</summary><div class="chart-scroll"><table><tr><th>场景</th><th>路线／真值</th><th>Top-K／候选数</th><th>查询／执行</th><th>客户端并发上限</th><th>{{if eq .MeasurementMode "observe"}}SQL 错误{{else}}失败（SQL / 断言）{{end}}</th><th>P50 / P90 / P95 / P99 ms</th><th>查询 QPS</th><th>{{if eq .MeasurementMode "observe"}}质量均值{{else}}质量均值／阈值{{end}}</th></tr>{{range .Scenarios}}<tr><td>{{scenarioName .ID}}{{range .SessionSQL}}<br><small><code>{{.}}</code></small>{{end}}</td><td>{{.Route}} / {{.Oracle}}</td><td>{{.TopK}} / {{.CandidateK}}</td><td>{{.SelectedQueries}} / {{.Executions}}</td><td>{{.EffectiveConcurrency}}</td><td>{{if eq .QualityMode "observe"}}{{.SQLFailures}}{{else}}{{.Failures}}{{if .Repetitions}}（{{.SQLFailures}} / {{.AssertionFailures}}）{{end}}{{end}}{{if .Error}}<br><small class="fail">{{.Error}}</small>{{end}}</td><td>{{if and .Repetitions (eq .SQLSuccesses 0)}}暂无样本{{else}}{{printf "%.1f / %.1f / %.1f / %.1f" .P50MS .P90MS .P95MS .P99MS}}{{end}}</td><td>{{if and .Repetitions (eq .SQLSuccesses 0)}}暂无样本{{else}}{{printf "%.1f" .QPS}}{{end}}</td><td>{{if and .Repetitions (eq .SQLSuccesses 0)}}暂无样本{{else if eq .QualityMode "observe"}}{{printf "%.3f" .MeanScore}}{{else}}{{printf "%.3f / %.3f" .MeanScore .MinScore}}{{end}}</td></tr>{{end}}</table></div></details>
{{if .Errors}}<details class="detail-panel" id="report-errors">{{if .HiddenProfiles}}<summary class="fail">当前展示范围的未通过检查</summary>{{range .Scenarios}}{{if or .Failures .Error}}<p class="fail">{{scenarioName .ID}} · 客户端并发 {{.EffectiveConcurrency}} · 失败执行 {{.Failures}}{{if .Error}}：{{.Error}}{{end}}</p>{{end}}{{end}}<p>完整原始运行的错误记录见 <a href="report.json">原始 JSON</a>。</p>{{else}}<summary class="fail">{{if eq .MeasurementMode "observe"}}运行错误{{else}}错误与未通过断言{{end}} · {{len .Errors}} 条记录</summary>{{range .Errors}}<p class="fail">{{.}}</p>{{end}}{{end}}</details>{{end}}
<details class="detail-panel measurement-details"><summary>运行参数、数据准备与测量身份</summary><p>数据集：{{.DisplayName}}<br>原始标识：<code>{{.Dataset}}</code><br>MO 版本：<code>{{.MatrixOneVersion}}</code><br>工具版本：<code>{{.ToolVersion}}</code><br>工具 SHA-256：<code>{{.BinarySHA256}}</code><br>数据清单 SHA-256：<code>{{.ManifestSHA256}}</code><br>测试库：<code>{{.Database}}</code>（{{.Cleanup}}）<br>资源指标：{{.ResourceMetrics}}</p><!-- profile --><h3>数据准备</h3><table><tr><th>阶段</th><th>秒</th><th>行数</th><th>错误</th></tr>{{range .Stages}}<tr><td>{{.Name}}</td><td>{{printf "%.2f" .Seconds}}</td><td>{{.Rows}}</td><td>{{.Error}}</td></tr>{{end}}</table>{{if .IndexSQL}}<h3>实测索引配置</h3>{{range .IndexSQL}}<pre>{{.}}</pre>{{end}}{{end}}<h3>输入文件</h3><table><tr><th>文件</th><th>SHA-256</th><th>预期行数</th></tr>{{range .Inputs}}<tr><td>{{.Path}}</td><td><code>{{.SHA256}}</code></td><td>{{.Rows}}</td></tr>{{end}}</table></details>
<p class="chart-note">逐查询 ID、返回结果、SQL、执行计划和校验值见 <a href="report.json">report.json</a>。图表由保存的原始记录生成，无外部请求。展示版本：<code>{{.RendererVersion}}</code>。</p></section>
<section id="report-environment" role="tabpanel" aria-labelledby="tab-environment" data-report-panel><!-- environment-tab --></section>
<!-- report-tabs-script --></main></body></html>`

const stabilityHTML = `<!-- stability:start -->{{if .StabilityCharts}}<section id="stability" class="stability-section"><div class="section-heading"><h2>重复结果稳定性</h2><span class="section-kicker">相同输入 · 集合与可选顺序检查</span></div><p class="chart-note">每行一条查询，每格一次执行或一段连续执行。{{if eq .MeasurementMode "observe"}}记录集合与顺序变化，同距结果换位也会记录；变化不计为运行失败。{{else}}集合比较保留重复 ID 的数量；启用顺序检查的场景还要求顺序一致。绿色仅表示本次重复与断言通过，召回质量由前面的质量图和独立场景验证。{{end}}</p>
{{range .StabilityCharts}}<article class="card stability-chart"><h3>{{.DisplayName}}</h3><p class="chart-note">{{.SelectedQueries}} 条查询{{if .Repetitions}}，每条重复 {{.Repetitions}} 次{{end}} · 客户端并发 {{.EffectiveConcurrency}}{{if .TopK}} · 返回 Top-{{.TopK}}{{end}}{{if .CheckOrder}} · 检查顺序{{end}}{{if .AllowEmpty}} · 允许稳定的空结果{{end}}{{if .Stability}} · 最低 ID 重合率 {{printf "%.3f" .WorstOverlap}} · 最多变化 ID {{.MaxChangedIDs}}{{end}}</p>{{if .Error}}<p class="fail">{{.Error}}</p>{{end}}
<p class="chart-note">{{stabilitySummary .ScenarioReport}}</p>
<!-- matrix:start -->{{template "stabilityMatrix" .Matrix}}<!-- matrix:end -->
<details><summary>查看逐查询数值、场景和物理计划</summary><p class="case-id">场景：<code>{{.ID}}</code></p>{{range .SessionSQL}}<p><code>{{.}}</code></p>{{end}}{{if .Stability}}<p>最多不同结果集合：<strong>{{.MaxDistinctResults}}</strong> · 最低 ID 重合率：<strong>{{printf "%.3f" .WorstOverlap}}</strong> · 最多变化 ID：<strong>{{.MaxChangedIDs}}</strong></p><div class="chart-scroll"><table class="stability-table">{{$observed := eq .QualityMode "observe"}}<tr><th>查询</th><th>成功 SQL / 执行</th><th>不同结果集合</th><th>不同顺序</th><th>最低 ID 重合率</th><th>最多变化 ID</th><th>{{if $observed}}SQL 错误{{else}}失败执行{{end}}</th><th>{{if $observed}}顺序变化次数{{else}}判定{{end}}</th></tr>{{range .Stability}}<tr><td>{{.QueryID}}</td><td>{{.SQLSuccesses}} / {{.Executions}}</td><td>{{.DistinctResults}}</td><td>{{if .DistinctOrders}}{{.DistinctOrders}}{{else}}—{{end}}</td><td>{{printf "%.3f" .WorstOverlap}}</td><td>{{.MaxChangedIDs}}</td><td>{{.Failures}}</td><td>{{if $observed}}{{.ReorderedExecutions}}{{else if eq .Executions 0}}未执行{{else if gt .Failures 0}}<strong class="fail">失败</strong>{{else}}<strong class="pass">通过</strong>{{end}}</td></tr>{{end}}</table></div>{{else}}<p>未执行重复结果比较。</p>{{end}}<p>具备独立正确结果的查询：{{.ExactTruthQueries}} / {{.SelectedQueries}}</p>{{range $endpoint, $plan := .PhysicalPlans}}<details class="stability-detail"><summary>物理计划 · {{$endpoint}}</summary><pre>{{$plan}}</pre></details>{{end}}<p class="chart-note">{{if eq .QualityMode "observe"}}逐次变化和结果差异保存在 observations；{{else}}SQL 错误和断言失败均计为失败；{{end}}逐次 ID、端点、版本与错误见 <a href="report.json">原始记录</a>。端点数不代表 CN 数量。</p></details></article>{{end}}</section>{{end}}<!-- stability:end -->`

func writeReport(dir string, report Report) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	jsonBody, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	jsonBody = append(jsonBody, '\n')
	jsonFile, err := os.OpenFile(filepath.Join(dir, "report.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := jsonFile.Write(jsonBody); err != nil {
		_ = jsonFile.Close()
		return err
	}
	if err := jsonFile.Close(); err != nil {
		return err
	}
	if report.Environment != nil {
		environmentBody, err := json.MarshalIndent(report.Environment, "", "  ")
		if err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(dir, "environment.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(append(environmentBody, '\n'))
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, "report.html"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if err := renderReportHTML(f, report); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func renderReportHTML(w io.Writer, report Report, levels ...int) error {
	return renderSelectedReportHTML(w, report, reportSelection{Levels: levels})
}

func renderSelectedReportHTML(w io.Writer, report Report, selection reportSelection) error {
	view, err := makeSelectedReportView(report, selection)
	if err != nil {
		return err
	}
	body := reportHTML
	if report.RunKind == "environment_inspection" {
		head, _, _ := strings.Cut(body, "</style></head>")
		body = strings.Replace(head, "检索现场报告", "运行环境检查", 1) + "</style></head>" + inspectionBody
	}
	body = strings.Replace(body, "<!-- chart-style -->", latencyStyle+loadStyle+overviewStyle+matrixStyle+queryQualityStyle+findingsStyle, 1)
	body = strings.Replace(body, "</style></head>", "</style><style id=\"environment-style\">"+environmentStyle+"</style><style id=\"report-tabs-style\">"+reportTabsStyle+"</style></head>", 1)
	body = strings.Replace(body, "<!-- environment -->", environmentHTML, 1)
	body = strings.Replace(body, "<!-- environment-tab -->", environmentTabHTML, 1)
	body = strings.Replace(body, "<!-- report-tabs -->", reportTabsHTML, 1)
	body = strings.Replace(body, "<!-- report-tabs-script -->", reportTabsScript, 1)
	body = strings.Replace(body, "<!-- retrieval-quality -->", retrievalQualityHTML, 1)
	body = strings.Replace(body, "<!-- overview -->", overviewHTML, 1)
	body = strings.Replace(body, "<!-- quality -->", queryQualityHTML, 1)
	body = strings.Replace(body, "<!-- stability -->", stabilityHTML, 1)
	body = strings.Replace(body, "<!-- concurrency -->", concurrencyHTML, 1)
	body = strings.ReplaceAll(body, "<!-- latency -->", latencyHTML)
	body = strings.Replace(body, "<!-- profile -->", profileHTML, 1)
	body += matrixHTML

	t, err := template.New("report").Funcs(template.FuncMap{"scenarioName": scenarioDisplayName, "stabilitySummary": stabilityChangeSummary}).Parse(body)
	if err != nil {
		return err
	}
	return t.Execute(w, view)
}

func renderSavedReport(dir string, levels ...int) error {
	return renderSelectedSavedReport(dir, reportSelection{Levels: levels})
}

func renderSelectedSavedReport(dir string, selection reportSelection) error {
	data, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		return err
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return fmt.Errorf("decode report.json: %w", err)
	}
	if report.Dataset == "" || (report.Status != "passed" && report.Status != "failed") {
		return fmt.Errorf("report.json is missing its dataset or valid run status")
	}
	f, err := os.CreateTemp(dir, ".report-*.html")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0o640); err != nil {
		return err
	}
	if err := renderSelectedReportHTML(f, report, selection); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "report.html"))
}
