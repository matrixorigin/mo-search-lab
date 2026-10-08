// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"fmt"
	"strconv"
	"strings"
)

type environmentMetadataCacheView struct {
	Label, Value, Source, Basis string
	nodeType, identity          string
}

func canonicalEnvironmentRole(role string) string {
	role = strings.ToUpper(role)
	if role == "DN" {
		return "TN"
	}
	return role
}

// Verified against the v4.2.1 release tag (d2393868a), pkg/objectio/cache.go.
// Keep version selection explicit: unknown builds must not inherit this rule.
func hasMetadataDefaultRule(server ServerEnvironment) bool {
	commit := strings.TrimSpace(server.GitCommit)
	if commit != "" {
		return len(commit) >= 8 && strings.HasPrefix("d2393868a7aaa6343518d80849fe4696aa3577e9", commit)
	}
	return strings.HasSuffix(server.Version, "-MatrixOne-v4.2.1")
}

func metadataDefaultCapacity(total int64) (int64, string) {
	const gib = int64(1 << 30)
	switch {
	case total < 2*gib:
		return total / 4, "系统总内存 <2 GiB，取 ¼"
	case total < 16*gib:
		return 512 << 20, "系统总内存 2～<16 GiB"
	case total < 32*gib:
		return gib, "系统总内存 16～<32 GiB"
	default:
		return 2 * gib, "系统总内存 ≥32 GiB"
	}
}

func declaredSystemMemory(e *EnvironmentEvidence, node ReportedNode) (*int64, string) {
	if e.Declared == nil {
		return nil, ""
	}
	for _, candidate := range e.Declared.Nodes {
		if canonicalEnvironmentRole(candidate.Role) == canonicalEnvironmentRole(node.NodeType) && candidate.ID == node.NodeID && candidate.SystemMemoryTotalBytes != nil {
			return candidate.SystemMemoryTotalBytes, candidate.SystemMemorySource
		}
	}
	return e.Declared.SystemMemoryTotalBytes, e.Declared.SystemMemorySource
}

func environmentMetadataDefault(e *EnvironmentEvidence, node ReportedNode) (value, basis, identity string) {
	if !hasMetadataDefaultRule(e.Server) {
		return "默认容量未知", "MO 版本默认规则未确认", "unknown-rule"
	}
	total, _ := declaredSystemMemory(e, node)
	if total == nil || *total <= 0 {
		return "默认容量未知", "缺少服务端系统总内存", "unknown-memory"
	}
	capacity, band := metadataDefaultCapacity(*total)
	return compactBytes(capacity) + "（环境默认，推算）", band + " · MO 4.2.1 默认规则", fmt.Sprintf("default:%d", capacity)
}

func metadataSettingWithEnvironment(raw string, e *EnvironmentEvidence, node ReportedNode) string {
	if raw == "0" || raw == "nil" {
		value, _, _ := environmentMetadataDefault(e, node)
		return value
	}
	return environmentSetting(raw, true)
}

func metadataCachePresentation(e *EnvironmentEvidence, node ReportedNode, setting ReportedConfig, present bool, files []string, fileDisplay string) environmentMetadataCacheView {
	raw, source, basis := setting.CurrentValue, "SQL 启动快照", "快照中的显式配置"
	if !present {
		source, basis = "TOML 文件", "文件显式值；运行时加载未确认"
		if len(files) == 0 {
			return environmentMetadataCacheView{Value: "未采集", Source: source, Basis: "配置来源缺失"}
		}
		raw = files[0]
		for _, candidate := range files[1:] {
			if candidate != raw {
				return environmentMetadataCacheView{Value: fileDisplay, Source: source, Basis: "多份文件配置有差异", identity: fmt.Sprintf("files:%q", files)}
			}
		}
		if raw == "not_set:" {
			raw = "0"
		}
		if _, err := strconv.ParseInt(raw, 10, 64); err != nil && raw != "nil" {
			return environmentMetadataCacheView{Value: fileDisplay, Source: source, Basis: "文件容量未识别", identity: fmt.Sprintf("files:%q", files)}
		}
	}
	row := environmentMetadataCacheView{Value: environmentSetting(raw, true), Source: source, Basis: basis, identity: raw}
	if raw == "0" || raw == "nil" {
		row.Value, row.Basis, row.identity = environmentMetadataDefault(e, node)
		row.Source += "＋代码默认值"
	}
	return row
}

func compactEnvironmentMetadataCaches(rows []environmentMetadataCacheView) []environmentMetadataCacheView {
	type key struct{ role, value, source, basis, identity string }
	indexes := make(map[key]int)
	members := make(map[int]map[string]bool)
	var result []environmentMetadataCacheView
	for _, row := range rows {
		k := key{row.nodeType, row.Value, row.Source, row.Basis, row.identity}
		if row.nodeType == "" {
			k.role = row.Label
		}
		i, ok := indexes[k]
		if !ok {
			i = len(result)
			indexes[k] = i
			result = append(result, row)
			members[i] = make(map[string]bool)
		}
		members[i][row.Label] = true
	}
	for i := range result {
		if count := len(members[i]); count > 1 {
			result[i].Label = fmt.Sprintf("%s ×%d", result[i].nodeType, count)
		}
	}
	return result
}
