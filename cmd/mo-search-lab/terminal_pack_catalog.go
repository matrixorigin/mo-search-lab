// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Presentation metadata never changes the pack identity or execution profile.
// A customer regression pack can supply this optional, bounded sidecar.
type terminalPackInfo struct {
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	Scale    string `json:"scale"`
	Featured bool   `json:"featured"`
}

func terminalPackScale(rows int64) string {
	if rows >= 100000 && rows%10000 == 0 {
		return fmt.Sprintf("%d 万行", rows/10000)
	}
	return terminalNumber(rows) + " 行"
}

func sameTerminalPackPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	left, e1 := filepath.Abs(a)
	right, e2 := filepath.Abs(b)
	return e1 == nil && e2 == nil && left == right
}

func describeTerminalPack(p *terminalPack, m manifest) {
	for _, ref := range m.Scenarios {
		var scene struct {
			Oracle string `json:"oracle"`
		}
		if previewPackJSON(p.Path, ref.Path, 64<<10, &scene) && scene.Oracle != "stable_multiset" {
			p.concurrent = true
		}
	}
	if m.Dataset == "gist1m_filtered_v7" {
		p.concurrent = false
	}
	p.Name, p.Summary, p.Scale = p.Dataset, "自定义测试；执行包内已定义的场景。", terminalPackScale(p.Rows)
	p.rank = 100
	has := func(name string) bool {
		for _, ref := range m.Scenarios {
			if filepath.Base(ref.Path) == name {
				return true
			}
		}
		return false
	}
	var first struct {
		TopK int `json:"top_k"`
	}
	if len(m.Scenarios) > 0 {
		previewPackJSON(p.Path, m.Scenarios[0].Path, 64<<10, &first)
	}
	switch {
	case p.Dataset == "gist1m_full":
		p.Name, p.Scale = "向量基础检查", "GIST1M · 100 万条 960 维向量"
		p.Summary = "向量检索与召回；返回条数以所选场景为准。"
		if first.TopK > 0 {
			p.Summary = fmt.Sprintf("向量检索与召回；每次返回 %d 条。", first.TopK)
		}
		if has("vector_nprobe_20.json") && has("vector_nprobe_100.json") && has("vector_stability.json") {
			p.Name = "向量性能与召回"
			p.Summary = fmt.Sprintf("召回、延迟、吞吐与重复稳定性；返回 %d 条，默认 1 并发，可勾选 4、8 并发。", first.TopK)
			// The Top-100 health profile is the current routine vector test.
			p.Featured, p.rank = first.TopK == 100, 0
		} else if len(m.Scenarios) > 1 {
			p.Name, p.Summary = "向量参数对比", "比较探测分组数对召回和延迟的影响。"
		}
		if !p.Featured && first.TopK > 0 {
			p.Name += fmt.Sprintf(" · Top-%d", first.TopK)
		}
	case p.Dataset == "gist1m_filtered_v7":
		p.Name, p.Scale = "向量过滤检查", "GIST1M · 100 万条 960 维向量"
		p.Summary = "比较先过滤 / 后过滤的召回、延迟与稳定性；可见比例 100%、10%、1%，串行执行。"
		p.Featured, p.rank = true, 1
	case p.Dataset == "t2ranking_anli_2303643_ngram_v3":
		p.Name, p.Scale = "全文检索评测", "T2Ranking · 2,303,643 中文段落 · 500 条查询"
		p.Summary = "anli 查询方式，ngram 索引；测相关性、延迟、并发和重复稳定性。"
		p.Featured, p.rank = true, 2
	case p.Dataset == "gist_t2_sql_workload_v7":
		p.Name, p.Scale = "两路同时查询", "100 万向量 + 230 万段落"
		p.Summary = "复用 GIST/T2 数据；向量与全文 SQL 同时执行，观察两路延迟和吞吐。"
		p.Featured, p.rank = true, 3
	case strings.HasPrefix(p.Dataset, "t2ranking_anli_"):
		p.Name = "全文检索 · anli"
		if strings.Contains(p.Dataset, "gojieba") {
			p.Name = "全文检索 · gojieba"
		}
		p.Summary = "anli 查询方式的全文对比或历史版本；具体身份见下方数据包目录。"
	case strings.HasPrefix(p.Dataset, "t2ranking_"):
		p.Name, p.Summary = "全文原始查询实验", "T2Ranking 原始查询形态的实验或历史版本。"
	case strings.HasPrefix(p.Dataset, "gist1m_subset"):
		p.Name, p.Summary = "向量小规模试验", "GIST 子集，用于参数与流程试验；不等于完整 GIST1M 召回评测。"
	case p.Dataset == "fulltext_functional_v1":
		p.Name, p.Summary = "全文功能验证", "小样本文本；验证分词、布尔搜索与空结果等功能。"
	case p.Dataset == "smoke_8":
		p.Name, p.Summary = "流程检查", "8 行小样本，检查导入、索引、查询和报告流程。"
	}
	var custom struct {
		Name     string `json:"name"`
		Summary  string `json:"summary"`
		Scale    string `json:"scale"`
		Featured *bool  `json:"featured"`
	}
	if previewPackJSON(p.Path, "pack-info.json", 16<<10, &custom) && strings.TrimSpace(custom.Name) != "" {
		p.Name = strings.TrimSpace(custom.Name)
		if custom.Summary != "" {
			p.Summary = custom.Summary
		}
		if custom.Scale != "" {
			p.Scale = custom.Scale
		}
		if custom.Featured != nil {
			p.Featured = *custom.Featured
		}
		if p.rank == 100 && p.Featured {
			p.rank = 50
		}
	}
}

func (p terminalPack) listScale() string {
	switch {
	case (p.Dataset == "gist1m_full" || p.Dataset == "gist1m_filtered_v7") && p.Rows == 1000000:
		return "100 万向量"
	case p.Dataset == "t2ranking_anli_2303643_ngram_v3" && p.Rows == 2303643:
		return "230 万段落"
	case p.Dataset == "gist_t2_sql_workload_v7":
		return "共用两份数据"
	}
	return terminalPackScale(p.Rows)
}

func (f *terminalLauncher) packIndices() []int {
	var common, all []int
	for i, p := range f.packs {
		all = append(all, i)
		if p.Featured {
			common = append(common, i)
		}
	}
	if f.allPacks || len(common) == 0 {
		return all
	}
	return common
}

func (f *terminalLauncher) movePack(delta int) {
	choices := f.packChoices()
	for i, choice := range choices {
		if choice.Pack == f.packChoice && choice.Level == f.concurrencyChoice {
			choice = choices[max(0, min(len(choices)-1, i+delta))]
			f.packChoice, f.concurrencyChoice = choice.Pack, choice.Level
			return
		}
	}
	if len(choices) > 0 {
		f.packChoice, f.concurrencyChoice = choices[0].Pack, choices[0].Level
	}
}

func (f *terminalLauncher) openPackChooser() {
	f.choosing, f.allPacks = true, false
	f.beforePacks = append([]string{}, f.selectedPacks...)
	f.beforeMixed = f.fields[launchMixed].Value
	f.beforeConcurrency = cloneTerminalConcurrency(f.packConcurrency)
	f.packChoice = 0
	f.concurrencyChoice = 0
	for i, p := range f.packs {
		if f.packChecked(p.Path) && !p.Featured {
			f.allPacks = true
		}
		if sameTerminalPackPath(p.Path, f.fields[launchPack].Value) {
			f.packChoice = i
		}
	}
	f.movePack(0)
}

func (f *terminalLauncher) selectedPack() *terminalPack {
	path := f.fields[launchPack].Value
	if len(f.selectedPacks) > 0 {
		path = f.selectedPacks[0]
	}
	for i := range f.packs {
		if sameTerminalPackPath(f.packs[i].Path, path) {
			return &f.packs[i]
		}
	}
	return nil
}

func (f *terminalLauncher) packChecked(path string) bool {
	for _, selected := range f.selectedPacks {
		if sameTerminalPackPath(path, selected) {
			return true
		}
	}
	return false
}

func (f *terminalLauncher) setSelectedPacks(paths []string) {
	var selected []string
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		found := false
		for _, p := range f.packs {
			found = found || sameTerminalPackPath(p.Path, path)
		}
		if !found {
			p := terminalPack{Path: path, Dataset: "manual", terminalPackInfo: terminalPackInfo{Name: "指定数据包", Scale: "手动路径", Summary: "启动后校验此路径的数据包。"}}
			var m manifest
			if previewPackJSON(path, "manifest.json", 1<<20, &m) && safeIdentifier.MatchString(m.Dataset) {
				p.Dataset = m.Dataset
				for _, load := range m.Loads {
					p.Rows += max(0, load.Rows)
				}
				describeTerminalPack(&p, m)
			}
			f.packs = append(f.packs, p)
		}
	}
	// List order defines execution order, independent of checkbox click order.
	for _, p := range f.packs {
		for _, path := range paths {
			if sameTerminalPackPath(p.Path, path) {
				selected = append(selected, p.Path)
				break
			}
		}
	}
	f.selectedPacks = selected
	f.fields[launchPack].Value, f.fields[launchMixed].Value = "", ""
	if len(selected) > 0 {
		f.fields[launchPack].Value = selected[0]
	}
	if len(selected) == 1 && f.selectedPack().Dataset == "gist_t2_sql_workload_v7" {
		f.fields[launchMixed].Value = "vector,fulltext"
	}
}

func (f *terminalLauncher) togglePack(path string) {
	paths := append([]string{}, f.selectedPacks...)
	if f.packChecked(path) {
		delete(f.packConcurrency, terminalConcurrencyKey(path))
		paths = nil
		for _, selected := range f.selectedPacks {
			if !sameTerminalPackPath(path, selected) {
				paths = append(paths, selected)
			}
		}
	} else {
		paths = append(paths, path)
	}
	f.setSelectedPacks(paths)
	f.err = ""
}

func (f *terminalLauncher) toggleVisiblePacks() {
	indices, all := f.packIndices(), true
	for _, index := range indices {
		all = all && f.packChecked(f.packs[index].Path)
	}
	for _, index := range indices {
		if f.packChecked(f.packs[index].Path) == all {
			f.togglePack(f.packs[index].Path)
		}
	}
}

func (f *terminalLauncher) selectionLabel() string {
	if len(f.selectedPacks) == 0 {
		return "未勾选"
	}
	if len(f.selectedPacks) == 1 {
		return f.selectedPack().Name
	}
	common, checked := 0, true
	for _, p := range f.packs {
		if p.Featured {
			common++
			checked = checked && f.packChecked(p.Path)
		}
	}
	if common == len(f.selectedPacks) && checked || common == 0 && len(f.selectedPacks) == len(f.packs) {
		return fmt.Sprintf("全部 %d 项", len(f.selectedPacks))
	}
	return fmt.Sprintf("已选 %d 项", len(f.selectedPacks))
}

func (f *terminalLauncher) requests(seed options) ([]terminalRequest, error) {
	if !f.inspecting && len(f.selectedPacks) == 0 {
		return nil, fmt.Errorf("请至少勾选一项测试；Enter 或 Ctrl-P 打开列表")
	}
	if len(f.selectedPacks) > 128 {
		return nil, fmt.Errorf("一次最多运行 128 项测试")
	}
	base, err := f.request(seed)
	if err != nil {
		return nil, err
	}
	if base.Inspect {
		base.Label = "环境检查"
		return []terminalRequest{base}, nil
	}
	var requests []terminalRequest
	for i, path := range f.selectedPacks {
		request := base
		request.Options.pack = path
		request.Options.concurrencyLevels = f.levelsForPack(path)
		request.Options.defaultConcurrency = len(request.Options.concurrencyLevels) == 1
		for _, p := range f.packs {
			if !sameTerminalPackPath(p.Path, path) {
				continue
			}
			request.Label = p.Name
			if len(f.selectedPacks) > 1 {
				request.Options.reportDir = filepath.Join(base.Options.reportDir, fmt.Sprintf("%02d-%s", i+1, p.Dataset))
				request.Options.mixedScenarios = nil
				if p.Dataset == "gist_t2_sql_workload_v7" {
					request.Options.mixedScenarios = []string{"vector", "fulltext"}
				}
			}
			break
		}
		requests = append(requests, request)
	}
	return requests, nil
}
