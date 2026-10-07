// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"fmt"
	"strings"
)

type checkSummary struct {
	Health, Stability                                            string
	SQLSamples, SQLErrors, FunctionalFailures, StabilityFailures int
	QualitySamples                                               int
}

func summarizeChecks(r Report) checkSummary {
	out := checkSummary{Health: "通过", Stability: "未测量"}
	if len(r.Scenarios) == 0 {
		out.Health = "未完成"
	}
	for _, stage := range r.Stages {
		if stage.Error != "" {
			out.Health = "异常"
		}
	}
	if strings.HasPrefix(r.Cleanup, "failed") || r.Cleanup == "not_started" {
		out.Health = "未完成"
	}
	for _, s := range r.Scenarios {
		if len(s.Results) == 0 || s.PlanError != "" || s.Error != "" {
			out.Health = "未完成"
		}
		if s.Oracle == "stable_multiset" {
			out.Stability = "通过"
		}
		for _, result := range s.Results {
			if !result.SQLSucceeded {
				out.SQLErrors++
				continue
			}
			out.SQLSamples++
			if s.Oracle == "qrels" || s.Oracle == "ann_recall" {
				out.QualitySamples++
			} else if !result.Pass {
				if s.Oracle == "stable_multiset" {
					out.StabilityFailures++
				} else {
					out.FunctionalFailures++
				}
			}
		}
	}
	if out.SQLErrors > 0 || out.FunctionalFailures > 0 {
		out.Health = "异常"
	}
	for _, s := range r.Scenarios {
		if s.Oracle == "stable_multiset" && (s.Failures > 0 || s.Error != "" || len(s.Results) == 0) {
			out.Stability = "异常 / 未完成"
		}
	}
	if out.StabilityFailures > 0 {
		out.Stability = "有断言未通过"
	}
	return out
}

type retrievalQualityBar struct {
	Label        string
	Value, Width float64
	Y            int
}

type retrievalQualityRow struct {
	ID, Mode, Gain                       string
	Concurrency, Queries, Samples, Empty int
	Bars                                 []retrievalQualityBar
}

func buildRetrievalQualityRows(scenarios []ScenarioReport) []retrievalQualityRow {
	rows := make([]retrievalQualityRow, 0, len(scenarios))
	for _, s := range scenarios {
		if s.Oracle != "qrels" {
			continue
		}
		row := retrievalQualityRow{ID: s.ID, Concurrency: s.EffectiveConcurrency, Mode: "阈值检查", Gain: "指数收益"}
		if s.QualityMode == "observe" {
			row.Mode = "观察，无验收阈值"
		}
		if s.NDCGGain == "linear" {
			row.Gain = "线性收益"
		}
		ids := make(map[string]bool)
		var ndcg10, ndcg, recall, mrr float64
		for _, result := range s.Results {
			if !result.SQLSucceeded || result.Quality == nil {
				continue
			}
			row.Samples++
			ids[result.ID] = true
			if len(result.IDs) == 0 {
				row.Empty++
			}
			ndcg10 += result.Quality.NDCG10
			ndcg += result.Quality.NDCG
			recall += result.Quality.Recall
			mrr += result.Quality.MRR10
		}
		if row.Samples == 0 {
			continue
		}
		row.Queries = len(ids)
		row.Bars = []retrievalQualityBar{
			{Label: fmt.Sprintf("nDCG@%d", min(10, s.TopK)), Value: ndcg10},
			{Label: fmt.Sprintf("nDCG@%d", s.TopK), Value: ndcg},
			{Label: fmt.Sprintf("Recall@%d", s.TopK), Value: recall},
			{Label: "MRR@10", Value: mrr},
		}
		for i := range row.Bars {
			bar := &row.Bars[i]
			bar.Value /= float64(row.Samples)
			bar.Width = bar.Value * 460
			bar.Y = i * 32
		}
		rows = append(rows, row)
	}
	return rows
}

const retrievalQualityHTML = `<!-- retrieval-quality:start -->{{if .RetrievalQuality}}<section id="retrieval-quality"><div class="section-heading"><h2>检索质量观察</h2><span class="section-kicker">相关性 · 覆盖率 · 空结果</span></div>{{range .RetrievalQuality}}<article class="card"><h3>{{.ID}} · 并发 {{.Concurrency}}</h3><p class="chart-note">{{.Queries}} 条不同查询 · {{.Samples}} 次成功 SQL · 空结果 {{.Empty}} 次 · {{.Mode}} · nDCG {{.Gain}}</p><div class="chart-scroll"><svg viewBox="0 0 720 158" class="retrieval-quality-plot" role="img" aria-label="{{.ID}} 并发 {{.Concurrency}} 的相关性与召回率"><line x1="140" x2="140" y1="0" y2="126" class="chart-grid"/><line x1="600" x2="600" y1="0" y2="126" class="chart-grid"/>{{range .Bars}}<g transform="translate(0 {{.Y}})"><title>{{.Label}} {{printf "%.4f" .Value}}</title><text x="125" y="17" text-anchor="end">{{.Label}}</text><rect x="140" y="0" width="460" height="22" fill="#edf2f5"/><rect x="140" y="0" width="{{printf "%.2f" .Width}}" height="22" fill="#087f8c"/><text x="620" y="17">{{printf "%.4f" .Value}}</text></g>{{end}}<text x="140" y="150">0</text><text x="600" y="150">1</text></svg></div></article>{{end}}<p class="chart-note">成功 SQL 的评分均值，空结果以 0 计入；SQL 错误单独列出。Recall/MRR 的相关等级下限和 nDCG 收益方式保存在原始场景记录。未标注段落按 0 收益计分，不代表人工认定其无关。nDCG 不是准确率或召回率。</p></section>{{end}}<!-- retrieval-quality:end -->`

const findingsStyle = `.findings{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:14px;margin:24px 0}.finding{border-left:3px solid #193b55;background:#fff;padding:16px;overflow-wrap:anywhere}.finding span,.finding small{display:block;color:#5c6c7d;font-size:13px}.finding strong{display:block;font-size:25px;margin:6px 0}.retrieval-quality-plot{width:100%;min-width:520px;display:block}.retrieval-quality-plot text{font:13px ui-monospace,Consolas,monospace;fill:#294253}@media(max-width:760px){.findings{grid-template-columns:1fr}}`
