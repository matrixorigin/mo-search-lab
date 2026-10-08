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

type environmentPresentation struct {
	Summary, Monitoring        []environmentRow
	Retrieval                  []environmentRetrievalGroup
	Nodes                      []environmentNodeView
	Caches                     []environmentCacheView
	MetadataCaches             []environmentMetadataCacheView
	Configs                    []environmentConfigView
	Diagnostics                environmentDiagnostics
	ClientSummary              string
	MonitorStatus, MonitorNote string
	SnapshotNote               string
}

type environmentRetrievalGroup struct {
	Label string
	Rows  []environmentRow
}

const metadataCacheKey = "commonconfig.metacache.memorycapacity"

// A zero metacache override leaves MO's built-in capacity in use. Its actual
// capacity also depends on the server's memory and runtime hints, not SQL alone.
func metadataCacheSetting(raw string) string {
	if raw == "0" {
		return "使用环境默认（配置值 0）"
	}
	return environmentSetting(raw, true)
}

type environmentNodeView struct{ Label, ID, Address, State, Source string }
type environmentCacheView struct {
	Node, Service, Backend, Memory, Disk, Source string
	NodeCount                                    int
	nodeType, identity                           string
}
type environmentConfigView struct {
	Node, Parameter, Snapshot, Default, File string
	Difference                               string
	hasFile                                  bool
	nodeType, identity                       string
}
type environmentCacheGroup struct {
	Node      ReportedNode
	Service   string
	SQL       map[string]ReportedConfig
	File      map[string][]string
	FileExact map[string][]string
	FileRaw   map[string][]string
	Source    string
}

func compactEnvironmentCaches(rows []environmentCacheView) []environmentCacheView {
	type key struct{ role, service, backend, memory, disk, source, identity string }
	indexes := make(map[key]int)
	members := make(map[int]map[string]bool)
	var result []environmentCacheView
	for _, row := range rows {
		k := key{row.nodeType, row.Service, row.Backend, row.Memory, row.Disk, row.Source, row.identity}
		if row.nodeType == "" {
			k.role = row.Node
		}
		index, ok := indexes[k]
		if !ok {
			index = len(result)
			indexes[k] = index
			result = append(result, row)
			members[index] = make(map[string]bool)
		}
		members[index][row.Node] = true
	}
	for i := range result {
		result[i].NodeCount = len(members[i])
		if result[i].NodeCount > 1 {
			result[i].Node = fmt.Sprintf("%s ×%d", result[i].nodeType, result[i].NodeCount)
		}
	}
	return result
}

func compactEnvironmentConfigs(rows []environmentConfigView) []environmentConfigView {
	type key struct{ role, parameter, snapshot, defaults, file, identity, difference string }
	indexes := make(map[key]int)
	members := make(map[int]map[string]bool)
	var result []environmentConfigView
	for _, row := range rows {
		k := key{row.nodeType, row.Parameter, row.Snapshot, row.Default, row.File, row.identity, row.Difference}
		if row.nodeType == "" {
			k.role = row.Node
		}
		index, ok := indexes[k]
		if !ok {
			index = len(result)
			indexes[k] = index
			result = append(result, row)
			members[index] = make(map[string]bool)
		}
		members[index][row.Node] = true
	}
	for i := range result {
		if count := len(members[i]); count > 1 {
			result[i].Node = fmt.Sprintf("%s ×%d", result[i].nodeType, count)
		}
	}
	return result
}

func environmentConfigIdentity(setting ReportedConfig, present bool, files []string) string {
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	if present {
		return "SQL " + strconv.Quote(setting.CurrentValue) + " " + strconv.Quote(setting.DefaultValue) + fmt.Sprintf(" file %q", sorted)
	}
	return fmt.Sprintf("file %q", sorted)
}

func exactFileMemorySetting(value ConfiguredMemory) string {
	if value.Bytes != nil {
		return strconv.FormatInt(*value.Bytes, 10)
	}
	return value.Status + ":" + value.Raw
}

func compactBytes(bytes int64) string {
	if bytes < 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	value, unit := float64(bytes), 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 2, 64), "0"), ".") + " " + units[unit]
}

func environmentSetting(raw string, memory bool) string {
	if raw == "" {
		return "未返回"
	}
	if raw == "nil" {
		return "未设置"
	}
	if memory {
		if bytes, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return compactBytes(bytes)
		}
	}
	return raw
}

func fileMemorySetting(value ConfiguredMemory) string {
	if value.Status == "not_set" {
		return ""
	}
	if value.Bytes != nil {
		return compactBytes(*value.Bytes)
	}
	return value.Raw + "（容量未识别）"
}

func nodeOrder(role string) int {
	for i, name := range []string{"CN", "TN", "LOG", "PROXY"} {
		if role == name {
			return i
		}
	}
	return 4
}

func buildEnvironmentPresentation(report Report) environmentPresentation {
	v := environmentPresentation{MonitorStatus: "未采集", MonitorNote: "本记录没有可展示的资源曲线。"}
	e := report.Environment
	if e == nil {
		v.SnapshotNote = "此历史记录未采集节点配置快照。"
		v.Summary = []environmentRow{{"MO 版本", unknown(report.MatrixOneVersion), "SQL"}, {"MO 部署与资源", "未知；此历史记录未自动采集", "未提供"}}
		return v
	}
	v.Summary = []environmentRow{{"MO 版本", unknown(e.Server.Version), "SQL"}, {"SQL 入口", unknown(report.Profile.SQLAddress), "本次连接"}, {"构建时间 / Git", unknown(e.Server.BuildTime) + " / " + unknown(e.Server.GitCommit), "SQL"}}
	if d := e.Declared; d != nil {
		deployment := map[string]string{"docker": "Docker（非 K8s）", "kubernetes": "K8s", "bare-metal": "裸机", "unknown": "未确认"}[d.Deployment]
		if deployment == "" {
			deployment = "未提供"
		}
		v.Summary = append(v.Summary, environmentRow{"部署方式", deployment, "客户声明"})
		var resources []string
		if d.CPULimit != nil {
			resources = append(resources, fmt.Sprintf("%g CPU", *d.CPULimit))
		}
		if d.MemoryLimitBytes != nil {
			resources = append(resources, compactBytes(*d.MemoryLimitBytes))
		}
		if len(resources) > 0 {
			v.Summary = append(v.Summary, environmentRow{"资源配额", strings.Join(resources, " / "), "客户声明"})
		}
		if d.Scope != "" {
			v.Summary = append(v.Summary, environmentRow{"环境信息范围", d.Scope, "客户声明"})
		}
		for _, row := range []environmentRow{{"MO 主机", d.Host, "客户声明"}, {"MO 系统", d.OS, "客户声明"}, {"MO CPU 型号", d.CPUModel, "客户声明"}} {
			if row.Value != "" {
				v.Summary = append(v.Summary, row)
			}
		}
		v.Summary = append(v.Summary, declaredResourceRows(d.Nodes)...)

	} else {
		v.Summary = append(v.Summary, environmentRow{"部署与资源配额", "未提供", "部署信息"})
	}
	count := "未采集"
	if e.Server.VisibleCNCount != nil {
		count = fmt.Sprint(*e.Server.VisibleCNCount)
	}
	v.Summary = append(v.Summary, environmentRow{"SQL 可见 CN 数", count, "SQL / 当前账号"})
	if len(e.Server.ReportedNodes) > 0 {
		counts := make(map[string]int)
		for _, node := range e.Server.ReportedNodes {
			counts[node.NodeType]++
		}
		var roles, parts []string
		for role := range counts {
			roles = append(roles, role)
		}
		sort.Slice(roles, func(i, j int) bool {
			if nodeOrder(roles[i]) != nodeOrder(roles[j]) {
				return nodeOrder(roles[i]) < nodeOrder(roles[j])
			}
			return roles[i] < roles[j]
		})
		for _, role := range roles {
			parts = append(parts, fmt.Sprintf("%s %d", role, counts[role]))
		}
		v.Summary = append(v.Summary, environmentRow{"配置上报节点", strings.Join(parts, " / "), "SQL / HAKeeper"})
	}
	// Apply the current allowlist to historical records too, without rewriting them.
	var raw [][]string
	for _, item := range e.Server.ReportedConfig {
		raw = append(raw, []string{item.NodeType, item.NodeID, item.Name, item.CurrentValue, item.DefaultValue})
	}
	config, _ := parseReportedConfig(raw)
	nodes := make(map[ReportedNode]bool)
	for _, node := range e.Server.ReportedNodes {
		nodes[node] = true
	}
	for _, node := range e.Server.VisibleCNs {
		nodes[ReportedNode{"CN", node.ID}] = true
	}
	groups := make(map[string]*environmentCacheGroup)
	memory := make(map[ReportedNode]map[string]ReportedConfig)
	for _, item := range config {
		node := ReportedNode{item.NodeType, item.NodeID}
		nodes[node] = true
		if index, field, ok := reportedFileServiceParts(item.Name); ok {
			key := node.NodeType + "\x00" + node.NodeID + "\x00" + strconv.Itoa(index)
			if groups[key] == nil {
				groups[key] = &environmentCacheGroup{Node: node, SQL: make(map[string]ReportedConfig), File: make(map[string][]string), FileExact: make(map[string][]string), FileRaw: make(map[string][]string), Source: "SQL 启动快照"}
			}
			groups[key].SQL[field] = item
			if field == "name" {
				groups[key].Service = item.CurrentValue
			}
		} else {
			if memory[node] == nil {
				memory[node] = make(map[string]ReportedConfig)
			}
			memory[node][item.Name] = item
		}
	}
	fileMemory := make(map[ReportedNode]map[string][]string)
	fileMemoryExact := make(map[ReportedNode]map[string][]string)
	fileMemoryRaw := make(map[ReportedNode]map[string][]string)
	metadataFileRaw := make(map[ReportedNode][]string)
	for _, file := range e.Configs {
		node := ReportedNode{strings.ToUpper(file.ServiceType), file.NodeID}
		if node.NodeID == "" {
			node.NodeID = "文件 " + file.File.Path
		}
		for _, service := range file.FileServices {
			if excludedFileService(service.Name) {
				continue
			}
			nodes[node] = true
			var group *environmentCacheGroup
			for _, candidate := range groups {
				if candidate.Node == node && strings.EqualFold(candidate.Service, service.Name) {
					group = candidate
					break
				}
			}
			if group == nil {
				key := node.NodeType + "\x00" + node.NodeID + "\x00file:" + strings.ToUpper(service.Name)
				group = &environmentCacheGroup{Node: node, Service: service.Name, SQL: make(map[string]ReportedConfig), File: make(map[string][]string), FileExact: make(map[string][]string), FileRaw: make(map[string][]string), Source: "TOML 文件"}
				groups[key] = group
			}
			values := map[string]string{"backend": service.Backend, "cache.memorycapacity": fileMemorySetting(service.MemoryCapacity), "cache.diskcapacity": fileMemorySetting(service.DiskCapacity), "cache.diskpath": service.DiskPath}
			exact := map[string]string{"backend": service.Backend, "cache.memorycapacity": exactFileMemorySetting(service.MemoryCapacity), "cache.diskcapacity": exactFileMemorySetting(service.DiskCapacity), "cache.diskpath": service.DiskPath}
			for field, value := range values {
				if value != "" {
					group.File[field] = append(group.File[field], file.File.Path+": "+value)
					group.FileExact[field] = append(group.FileExact[field], file.File.Path+": "+exact[field])
					group.FileRaw[field] = append(group.FileRaw[field], exact[field])
				}
			}
		}
		for _, value := range file.Memory {
			field := map[string]string{"metacache.memory-capacity": metadataCacheKey, "cn.pipeline.host-size": "config.pipeline.hostsize", "cn.pipeline.guest-size": "config.pipeline.guestsize", "cn.pipeline.batch-size": "config.pipeline.batchsize", "cn.frontend.mempoolmaxsize": "config.frontend.mempoolmaxsize"}[strings.ToLower(value.Key)]
			if field == "" {
				continue
			}
			nodes[node] = true
			if fileMemory[node] == nil {
				fileMemory[node] = make(map[string][]string)
				fileMemoryExact[node] = make(map[string][]string)
				fileMemoryRaw[node] = make(map[string][]string)
			}
			display := fileMemorySetting(value)
			if field == metadataCacheKey {
				metadataFileRaw[node] = append(metadataFileRaw[node], exactFileMemorySetting(value))
				if value.Status == "not_set" {
					display = "未显式设置"
				} else if value.Bytes != nil {
					display = metadataCacheSetting(strconv.FormatInt(*value.Bytes, 10))
				}
			}
			fileMemory[node][field] = append(fileMemory[node][field], file.File.Path+": "+display)
			fileMemoryExact[node][field] = append(fileMemoryExact[node][field], file.File.Path+": "+exactFileMemorySetting(value))
			fileMemoryRaw[node][field] = append(fileMemoryRaw[node][field], exactFileMemorySetting(value))
		}
	}
	var ordered []ReportedNode
	for node := range nodes {
		ordered = append(ordered, node)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if nodeOrder(ordered[i].NodeType) != nodeOrder(ordered[j].NodeType) {
			return nodeOrder(ordered[i].NodeType) < nodeOrder(ordered[j].NodeType)
		}
		if ordered[i].NodeType != ordered[j].NodeType {
			return ordered[i].NodeType < ordered[j].NodeType
		}
		return ordered[i].NodeID < ordered[j].NodeID
	})
	labels, counts := make(map[ReportedNode]string), make(map[string]int)
	for _, node := range ordered {
		label := node.NodeType
		if strings.HasPrefix(node.NodeID, "文件 ") {
			label += " · " + node.NodeID
		} else {
			counts[node.NodeType]++
			label = fmt.Sprintf("%s %d", node.NodeType, counts[node.NodeType])
		}
		labels[node] = label
		identity := environmentNodeView{Label: label, ID: node.NodeID, Address: "未采集", State: "仅配置记录", Source: "SQL / HAKeeper"}
		if strings.HasPrefix(node.NodeID, "文件 ") {
			identity.Source = "TOML 文件"
		}
		for _, visible := range e.Server.VisibleCNs {
			if node.NodeType == "CN" && node.NodeID == visible.ID {
				identity.Address, identity.State = visible.Address, visible.State
				identity.Source = "SQL / 当前账号"
			}
		}
		v.Nodes = append(v.Nodes, identity)
	}
	for _, node := range ordered {
		role := node.NodeType
		if strings.HasPrefix(node.NodeID, "文件 ") {
			role = ""
		}
		var serviceGroups []*environmentCacheGroup
		for _, group := range groups {
			if group.Node == node {
				serviceGroups = append(serviceGroups, group)
			}
		}
		sort.Slice(serviceGroups, func(i, j int) bool { return serviceGroups[i].Service < serviceGroups[j].Service })
		for _, group := range serviceGroups {
			cache := environmentCacheView{Node: labels[node], Service: unknown(group.Service), Source: group.Source, nodeType: role}
			for _, field := range []struct {
				key, label string
				memory     bool
			}{{"backend", "后端", false}, {"cache.memorycapacity", "内存缓存容量", true}, {"cache.diskcapacity", "磁盘缓存容量", true}, {"cache.diskpath", "磁盘缓存路径", false}} {
				setting, present := group.SQL[field.key]
				if field.memory {
					if sized, ok := group.SQL[field.key+".bytesize"]; ok {
						setting, present = sized, true
					}
				} else if field.key == "cache.diskpath" {
					if path, ok := group.SQL[field.key+".string"]; ok {
						setting, present = path, true
					}
				}
				snapshot, defaultValue, display := "未采集", "未采集", "未采集"
				if present {
					snapshot, defaultValue = environmentSetting(setting.CurrentValue, field.memory), environmentSetting(setting.DefaultValue, field.memory)
					display = snapshot
				}
				fileValue := "未显式设置"
				if values := group.File[field.key]; len(values) > 0 {
					sort.Strings(values)
					fileValue = strings.Join(values, "\n")
					if !present {
						display = fileValue
					}
				}
				identity := environmentConfigIdentity(setting, present, group.FileExact[field.key])
				switch field.key {
				case "backend":
					cache.Backend = display
				case "cache.memorycapacity":
					cache.Memory = display
				case "cache.diskcapacity":
					cache.Disk = display
				}
				if field.key != "cache.diskpath" {
					cacheIdentity := identity
					if present {
						cacheIdentity = environmentConfigIdentity(setting, true, nil)
					}
					cache.identity += field.key + ":" + cacheIdentity + "\n"
				}
				if present || len(group.File[field.key]) > 0 {
					row := environmentConfigView{Node: labels[node], Parameter: group.Service + " · " + field.label, Snapshot: snapshot, Default: defaultValue, File: fileValue, nodeType: role, identity: identity}
					markEnvironmentConfigDifference(&row, setting, present, group.FileRaw[field.key], field.memory)
					v.Configs = append(v.Configs, row)
				}
			}
			v.Caches = append(v.Caches, cache)
		}
		var fields []string
		for field := range reportedMemoryFields {
			if _, ok := memory[node][field]; ok || len(fileMemory[node][field]) > 0 {
				fields = append(fields, field)
			}
		}
		sort.Strings(fields)
		for _, field := range fields {
			setting, present := memory[node][field]
			current, defaultValue, fileValue := "未采集", "未采集", "未显式设置"
			if present {
				current, defaultValue = environmentSetting(setting.CurrentValue, true), environmentSetting(setting.DefaultValue, true)
				if field == metadataCacheKey {
					current, defaultValue = metadataCacheSetting(setting.CurrentValue), metadataSettingWithEnvironment(setting.DefaultValue, e, node)
				}
			}
			if values := fileMemory[node][field]; len(values) > 0 {
				sort.Strings(values)
				fileValue = strings.Join(values, "\n")
			}
			if field == metadataCacheKey && !present {
				defaultValue, _, _ = environmentMetadataDefault(e, node)
			}
			row := environmentConfigView{Node: labels[node], Parameter: reportedMemoryFields[field], Snapshot: current, Default: defaultValue, File: fileValue, nodeType: role, identity: environmentConfigIdentity(setting, present, fileMemoryExact[node][field])}
			markEnvironmentConfigDifference(&row, setting, present, fileMemoryRaw[node][field], true)
			v.Configs = append(v.Configs, row)
			if field == metadataCacheKey {
				row := metadataCachePresentation(e, node, setting, present, metadataFileRaw[node], fileValue)
				row.Label, row.nodeType = labels[node], role
				v.MetadataCaches = append(v.MetadataCaches, row)
			}
		}
	}
	for _, group := range []environmentRetrievalGroup{
		{Label: "全文", Rows: []environmentRow{{Label: "相关性算法", Source: "ft_relevancy_algorithm"}, {Label: "Bloom Filter 下推", Source: "fulltext_bloom_filter_pushdown"}}},
		{Label: "IVF", Rows: []environmentRow{{Label: "探测分组数", Source: "probe_limit"}, {Label: "建索引线程参数", Source: "ivf_threads_build"}, {Label: "搜索线程参数", Source: "ivf_threads_search"}, {Label: "聚类训练采样比例（%）", Source: "kmeans_train_percent"}, {Label: "聚类最大迭代次数", Source: "kmeans_max_iteration"}}},
		{Label: "HNSW", Rows: []environmentRow{{Label: "建索引线程参数", Source: "hnsw_threads_build"}, {Label: "搜索线程参数", Source: "hnsw_threads_search"}, {Label: "索引容量参数", Source: "hnsw_max_index_capacity"}}},
		{Label: "向量过滤策略", Rows: []environmentRow{{Label: "默认启用 Pre-filter", Source: "enable_vector_prefilter_by_default"}, {Label: "默认启用 Auto 模式", Source: "enable_vector_auto_mode_by_default"}}},
	} {
		collected := environmentRetrievalGroup{Label: group.Label}
		for _, param := range group.Rows {
			if value, ok := e.Server.Variables[param.Source]; ok {
				param.Value = unknown(value)
				collected.Rows = append(collected.Rows, param)
			}
		}
		if len(collected.Rows) > 0 {
			v.Retrieval = append(v.Retrieval, collected)
		}
	}
	for _, param := range []struct{ key, label string }{{"connection_memory_limit", "单连接内存限制"}, {"global_connection_memory_limit", "全部连接内存限制"}, {"max_allowed_packet", "SQL 包大小上限"}} {
		if value, ok := e.Server.Variables[param.key]; ok {
			v.Configs = append(v.Configs, environmentConfigView{Node: "初始 SQL 会话", Parameter: param.label, Snapshot: environmentSetting(value, true), Default: "未采集", File: "不适用"})
		}
	}
	v.Caches = compactEnvironmentCaches(v.Caches)
	v.Configs = compactEnvironmentConfigs(v.Configs)
	v.MetadataCaches = compactEnvironmentMetadataCaches(v.MetadataCaches)
	v.SnapshotNote = "节点启动配置快照，上报时间未知；容量字段不证明缓存启用或实际占用。"
	if len(config) == 0 {
		v.SnapshotNote = "未取得节点配置快照；提供的 TOML 仅表示文件显式值。"
	}
	v.Diagnostics = buildEnvironmentDiagnostics(v, e)
	if report.RunKind != "environment_inspection" {
		v.ClientSummary = fmt.Sprintf("%s · %s/%s · %d 逻辑 CPU", unknown(e.Client.Hostname), e.Client.OS, e.Client.Arch, e.Client.LogicalCPUs)
	}

	m := e.Monitoring
	if m.Reason == "monitoring is not configured" {
		v.MonitorStatus, v.MonitorNote = "未接入", "未提供监控配置，未记录 CPU 和内存使用曲线。"
	} else if m.Status == "collected" {
		v.MonitorStatus, v.MonitorNote = "已采集", "查询阶段包含预热，导入和建索引不在监控时间窗内。"
	} else if m.Status == "partial" {
		v.MonitorStatus, v.MonitorNote = "部分采集", "部分指标未取得有效样本，详见各图与原始记录。"
	} else if m.Reason != "" && m.Reason != "monitoring is not configured" {
		v.MonitorStatus, v.MonitorNote = "未采集", m.Reason
		if strings.Contains(m.Reason, "inspect does not run") {
			v.MonitorNote = "本次只检查环境，未运行性能负载。"
		}
	}
	if m.Provider != "" {
		v.Monitoring = append(v.Monitoring, environmentRow{"监控来源", m.Provider + " · " + m.URL, "监控 API"}, environmentRow{"受测目标", m.Scope, "监控配置"})
	}
	if m.WindowStart != nil && m.WindowEnd != nil {
		v.Monitoring = append(v.Monitoring, environmentRow{"监控时间窗", m.WindowStart.UTC().Format("2006-01-02 15:04:05") + " ～ " + m.WindowEnd.UTC().Format("15:04:05") + " UTC", "查询阶段"})
	}
	return v
}
