// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// These are service-reported configuration snapshots from HAKeeper, including
// defaults. They are not a live memory measurement or proof of dynamic reload.
type ReportedNode struct {
	NodeType string `json:"node_type"`
	NodeID   string `json:"node_id"`
}

type ReportedConfig struct {
	NodeType     string `json:"node_type"`
	NodeID       string `json:"node_id"`
	Name         string `json:"name"`
	CurrentValue string `json:"current_value"`
	DefaultValue string `json:"default_value"`
}

const environmentReportedNodesSQL = "SELECT DISTINCT node_type, node_id FROM mo_catalog.mo_configurations ORDER BY node_type, node_id LIMIT 257"

var reportedFileServiceFields = []string{
	"name", "backend", "cache.memorycapacity", "cache.memorycapacity.bytesize",
	"cache.diskcapacity", "cache.diskcapacity.bytesize", "cache.diskpath",
	"cache.diskpath.string",
}

var reportedMemoryFields = map[string]string{
	"commonconfig.limit.memory":             "MO 内存上限",
	"commonconfig.metacache.memorycapacity": "Metadata 缓存容量",
	"config.frontend.mempoolmaxsize":        "Frontend 内存池上限",
	"config.pipeline.hostsize":              "Pipeline host-size",
	"config.pipeline.guestsize":             "Pipeline guest-size",
	"config.pipeline.batchsize":             "Pipeline batch-size",
}

var environmentReportedConfigSQL = buildReportedConfigSQL()
var reportedFileServiceKey = regexp.MustCompile(`^commonconfig\.fileservices\[([0-9]+)\]\.config\.(.+)$`)

func buildReportedConfigSQL() string {
	var predicates []string
	for _, field := range reportedFileServiceFields {
		predicates = append(predicates, "c.name LIKE 'commonconfig.fileservices[%].config."+field+"'")
	}
	var names []string
	for name := range reportedMemoryFields {
		names = append(names, "'"+name+"'")
	}
	sort.Strings(names)
	services := "(" + strings.Join(predicates, " OR ") + ") AND EXISTS (SELECT 1 FROM mo_catalog.mo_configurations fs WHERE fs.node_type = c.node_type AND fs.node_id = c.node_id AND fs.name LIKE 'commonconfig.fileservices[%].config.name' AND LOWER(TRIM(fs.current_value)) NOT IN ('etl', 'tmp') AND SUBSTRING_INDEX(fs.name, '.config.', 1) = SUBSTRING_INDEX(c.name, '.config.', 1))"
	// Filter at the server, then apply the same allowlist to decoded rows. Never
	// query SELECT *: this view may also contain storage passwords and keys.
	return "SELECT c.node_type, c.node_id, c.name, c.current_value, c.default_value FROM mo_catalog.mo_configurations c WHERE c.name IN (" + strings.Join(names, ", ") + ") OR (" + services + ") ORDER BY c.node_type, c.node_id, c.name LIMIT 2049"
}

func excludedFileService(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	return name == "ETL" || name == "TMP"
}

func reportedFileServiceParts(key string) (int, string, bool) {
	match := reportedFileServiceKey.FindStringSubmatch(strings.ToLower(key))
	if match == nil {
		return 0, "", false
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || index < 0 || index >= 64 {
		return 0, "", false
	}
	for _, field := range reportedFileServiceFields {
		if match[2] == field {
			return index, field, true
		}
	}
	return 0, "", false
}

func parseReportedNodes(rows [][]string) ([]ReportedNode, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("no service-reported nodes returned")
	}
	var nodes []ReportedNode
	seen := make(map[ReportedNode]bool)
	for _, row := range rows {
		if len(row) != 2 || row[0] == "" || row[1] == "" {
			return nil, fmt.Errorf("unexpected service-reported node columns")
		}
		node := ReportedNode{strings.ToUpper(row[0]), row[1]}
		if !seen[node] {
			nodes = append(nodes, node)
			seen[node] = true
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].NodeType != nodes[j].NodeType {
			return nodes[i].NodeType < nodes[j].NodeType
		}
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return nodes, nil
}

func parseReportedConfig(rows [][]string) ([]ReportedConfig, error) {
	var configs []ReportedConfig
	seen := make(map[string]bool)
	services := make(map[string]bool)
	serviceKey := func(row []string, index int) string {
		return strings.ToUpper(row[0]) + "\x00" + row[1] + "\x00" + strconv.Itoa(index)
	}
	for _, row := range rows {
		if len(row) != 5 || row[0] == "" || row[1] == "" {
			return nil, fmt.Errorf("unexpected service-reported configuration columns")
		}
		if index, field, ok := reportedFileServiceParts(row[2]); ok && field == "name" {
			name := strings.TrimSpace(row[3])
			services[serviceKey(row, index)] = name != "" && name != "nil" && !excludedFileService(name)
		}
	}
	for _, row := range rows {
		if len(row) != 5 || row[0] == "" || row[1] == "" {
			return nil, fmt.Errorf("unexpected service-reported configuration columns")
		}
		name := strings.ToLower(row[2])
		index, _, cacheField := reportedFileServiceParts(name)
		if cacheField && !services[serviceKey(row, index)] {
			continue
		}
		if _, memoryField := reportedMemoryFields[name]; !memoryField && !cacheField {
			continue
		}
		key := strings.ToUpper(row[0]) + "\x00" + row[1] + "\x00" + name
		if seen[key] {
			return nil, fmt.Errorf("duplicate service-reported configuration key")
		}
		seen[key] = true
		configs = append(configs, ReportedConfig{strings.ToUpper(row[0]), row[1], name, row[3], row[4]})
	}
	if len(configs) == 0 {
		return nil, fmt.Errorf("no allowlisted service-reported configuration returned")
	}
	sort.Slice(configs, func(i, j int) bool {
		a, b := configs[i], configs[j]
		if a.NodeType != b.NodeType {
			return a.NodeType < b.NodeType
		}
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		return a.Name < b.Name
	})
	return configs, nil
}
