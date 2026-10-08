// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type environmentDiagnostics struct {
	Configs       []environmentConfigView
	Nodes         []environmentNodeView
	Missing       []environmentRow
	NodeNotice    string
	HasFileValues bool
	Count         int
}

func equalEnvironmentRawValue(a, b string) bool {
	x, errX := strconv.ParseInt(a, 10, 64)
	y, errY := strconv.ParseInt(b, 10, 64)
	if errX == nil && errY == nil {
		return x == y
	}
	return a == b
}

func markEnvironmentConfigDifference(row *environmentConfigView, setting ReportedConfig, present bool, files []string, memory bool) {
	if !present {
		return
	}
	var reasons []string
	if setting.CurrentValue != "" && setting.DefaultValue != "" && !equalEnvironmentRawValue(setting.CurrentValue, setting.DefaultValue) {
		reasons = append(reasons, "与默认值不同")
		if memory && row.Snapshot == row.Default {
			row.Snapshot += "（" + setting.CurrentValue + " B）"
			row.Default += "（" + setting.DefaultValue + " B）"
		}
	}
	for _, file := range files {
		if strings.HasPrefix(file, "not_set:") {
			continue
		}
		row.hasFile = true
		if !equalEnvironmentRawValue(setting.CurrentValue, file) {
			reason := "与文件显式值不同"
			if memory && environmentSetting(setting.CurrentValue, true) == environmentSetting(file, true) {
				reason += "（快照 " + setting.CurrentValue + " B，文件 " + file + " B）"
			}
			reasons = append(reasons, reason)
			break
		}
	}
	row.Difference = strings.Join(reasons, "；")
}

func buildEnvironmentDiagnostics(v environmentPresentation, e *EnvironmentEvidence) environmentDiagnostics {
	d := environmentDiagnostics{}
	for _, row := range v.Configs {
		if row.Difference != "" {
			d.Configs = append(d.Configs, row)
			d.HasFileValues = d.HasFileValues || row.hasFile
		}
	}
	for _, node := range v.Nodes {
		if node.Source == "SQL / 当前账号" && node.State != "" && !strings.EqualFold(node.State, "Working") {
			d.Nodes = append(d.Nodes, node)
		}
	}
	if len(d.Nodes) > 0 {
		states := make(map[string]int)
		for _, node := range d.Nodes {
			states[node.State]++
		}
		var names, parts []string
		for state := range states {
			names = append(names, state)
		}
		sort.Strings(names)
		for _, state := range names {
			parts = append(parts, fmt.Sprintf("%d 个 CN 为 %s", states[state], state))
		}
		d.NodeNotice = "本次采集：" + strings.Join(parts, "，") + "。节点信息见诊断详情。"
	}
	for _, probe := range e.Server.Probes {
		if probe.Status == "collected" {
			continue
		}
		label := map[string]string{environmentBuildSQL: "构建信息", environmentVariablesSQL: "检索参数", environmentTopologySQL: "SQL 可见 CN", environmentReportedNodesSQL: "配置上报节点", environmentReportedConfigSQL: "节点配置快照"}[probe.SQL]
		if strings.HasPrefix(probe.SQL, "SHOW VARIABLES ") {
			label = "检索参数"
		} else if label == "" && strings.Contains(probe.SQL, "mo_catalog.mo_configurations") {
			label = "节点配置快照"
		}
		if label == "" {
			label = "SQL 探测"
		}
		d.Missing = append(d.Missing, environmentRow{label, unknown(probe.Error), "采集未完成"})
	}
	d.Count = len(d.Configs) + len(d.Nodes) + len(d.Missing)
	return d
}

// Preserve declared CPU/memory quotas without repeating node UUIDs.
func declaredResourceRows(nodes []DeclaredNode) []environmentRow {
	type key struct{ role, cpu, memory string }
	counts := make(map[key]int)
	for _, node := range nodes {
		if node.CPULimit == nil && node.MemoryLimitBytes == nil {
			continue
		}
		k := key{role: canonicalEnvironmentRole(node.Role)}
		if node.CPULimit != nil {
			k.cpu = strconv.FormatFloat(*node.CPULimit, 'g', -1, 64)
		}
		if node.MemoryLimitBytes != nil {
			k.memory = strconv.FormatInt(*node.MemoryLimitBytes, 10)
		}
		counts[k]++
	}
	var keys []key
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if nodeOrder(keys[i].role) != nodeOrder(keys[j].role) {
			return nodeOrder(keys[i].role) < nodeOrder(keys[j].role)
		}
		if keys[i].role != keys[j].role {
			return keys[i].role < keys[j].role
		}
		if keys[i].cpu != keys[j].cpu {
			return keys[i].cpu < keys[j].cpu
		}
		return keys[i].memory < keys[j].memory
	})
	var rows []environmentRow
	for _, k := range keys {
		var parts []string
		if k.cpu != "" {
			parts = append(parts, k.cpu+" CPU")
		}
		if k.memory != "" {
			bytes, _ := strconv.ParseInt(k.memory, 10, 64)
			parts = append(parts, compactBytes(bytes))
		}
		label := fmt.Sprintf("%s ×%d 资源配额", k.role, counts[k])
		rows = append(rows, environmentRow{label, strings.Join(parts, " / ") + "（每节点）", "客户声明"})
	}
	return rows
}
