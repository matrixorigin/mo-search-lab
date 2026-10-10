// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Display names describe measurements without changing their recorded identity.
// Unknown customer datasets and scenarios retain their own identifiers.
func datasetDisplayName(id, name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	switch {
	case id == "gist1m_full":
		return "GIST1M · 向量性能与召回"
	case id == "gist1m_filtered_v7":
		return "GIST1M · 向量过滤检查"
	case id == "gist_t2_sql_workload_v7":
		return "GIST + T2Ranking · 两路同时查询"
	case id == "t2ranking_anli_2303643_ngram_v3":
		return "T2Ranking · 全文检索评测"
	case strings.HasPrefix(id, "t2ranking_anli_"):
		if strings.Contains(id, "gojieba") {
			return "T2Ranking · anli 全文检索（jieba）"
		}
		return "T2Ranking · anli 全文检索"
	case strings.HasPrefix(id, "t2ranking_"):
		return "T2Ranking · 原始查询实验"
	case strings.HasPrefix(id, "gist1m_subset"):
		return "GIST · 小规模向量试验"
	case id == "fulltext_functional_v1":
		return "全文功能验证"
	case id == "smoke_8":
		return "流程检查 · 8 行样本"
	case id == "environment-inspection":
		return "运行环境检查"
	}
	return id
}

func scenarioDisplayName(id string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(id, "_mixed"), "_stability")
	name := ""
	switch base {
	case "vector":
		name = "向量检索"
	case "fulltext":
		name = "全文检索"
	case "anli_sql_tfidf":
		name = "全文检索 · TF-IDF"
	case "anli_sql_bm25":
		name = "全文检索 · BM25"
	case "raw_sentence_tfidf":
		name = "整句查询 · TF-IDF"
	case "anli_tokens_tfidf":
		name = "分词查询 · TF-IDF"
	case "fulltext_qrels":
		name = "全文相关性评测"
	case "load_exact":
		name = "数据导入核对"
	case "semantic_ngram":
		name = "全文功能 · ngram"
	case "semantic_gojieba":
		name = "全文功能 · jieba"
	case "session_probe":
		name = "会话参数检查"
	default:
		for _, rule := range []struct{ prefix, label string }{
			{"vector_pre_", "先过滤 · 可见 %d%%"},
			{"vector_post_", "后过滤 · 可见 %d%%"},
			{"vector_nprobe_", "向量检索 · 探测 %d 组"},
		} {
			if strings.HasPrefix(base, rule.prefix) {
				n, err := strconv.Atoi(strings.TrimPrefix(base, rule.prefix))
				if err == nil && n > 0 && (rule.prefix == "vector_nprobe_" || n <= 100) {
					name = fmt.Sprintf(rule.label, n)
				}
				break
			}
		}
	}
	if name == "" {
		return id
	}
	if strings.HasSuffix(id, "_stability") {
		name += " · 重复稳定性"
	}
	if strings.HasSuffix(id, "_mixed") {
		name += " · 两路同时运行"
	}
	return name
}

func (r Report) DisplayName() string      { return datasetDisplayName(r.Dataset, r.DatasetName) }
func (r terminalRun) DisplayName() string { return datasetDisplayName(r.Dataset, r.DatasetName) }

// The baseline is the first successful execution of each query. Compare ID
// multisets (including duplicate counts) separately from their returned order.
func stabilityChangeSummary(s ScenarioReport) string {
	baselines := make(map[string][]string)
	sets, orderOnly, sqlErrors, comparisons := 0, 0, 0, 0
	for _, result := range s.Results {
		if !result.SQLSucceeded {
			sqlErrors++
			continue
		}
		first, seen := baselines[result.ID]
		if !seen {
			baselines[result.ID] = result.IDs
			continue
		}
		comparisons++
		if multisetSignature(first) != multisetSignature(result.IDs) {
			sets++
		} else if !slices.Equal(first, result.IDs) {
			orderOnly++
		}
	}
	if len(baselines) == 0 {
		return fmt.Sprintf("暂无成功结果可比较；SQL 错误 %d 次。", sqlErrors)
	}
	if comparisons == 0 {
		return fmt.Sprintf("仅有首次成功结果，暂无重复比较；SQL 错误 %d 次。", sqlErrors)
	}
	line := fmt.Sprintf("与各查询首次成功结果比较：集合变化 %d 次 · 仅顺序变化 %d 次 · SQL 错误 %d 次。", sets, orderOnly, sqlErrors)
	if s.QualityMode != "observe" && s.CheckOrder && orderOnly > 0 {
		line += "旧版启用了严格顺序断言；集合相同的换位也会判为失败。"
	}
	return line
}
