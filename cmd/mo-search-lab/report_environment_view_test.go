// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestEnvironmentMetadataDefaultsAndPartialRetrievalEvidence(t *testing.T) {
	e := newEnvironmentEvidence(environmentInputs{})
	e.Server.GitCommit = "d2393868a"
	total := int64(64 << 30)
	e.Declared = &DeclaredEnvironment{SystemMemoryTotalBytes: &total, Scope: "MO nodes on the declared host"}
	e.Server.ReportedConfig = []ReportedConfig{
		{"CN", "a", metadataCacheKey, "0", "0"},
		{"CN", "b", metadataCacheKey, "0", "0"},
		{"LOG", "c", metadataCacheKey, "1073741824", "0"},
	}
	zero := int64(0)
	e.Configs = []MOConfigEvidence{{ServiceType: "TN", NodeID: "d", File: fileRef{Path: "tn.toml"}, Memory: []ConfiguredMemory{{Key: "metacache.memory-capacity", Status: "configured", Bytes: &zero}}}}
	e.Server.Variables = map[string]string{"probe_limit": "5", "kmeans_train_percent": "10", "enable_vector_prefilter_by_default": "off"}
	r := Report{Environment: e}
	before, _ := json.Marshal(r)
	v := buildEnvironmentPresentation(r)
	if len(v.MetadataCaches) != 3 || v.MetadataCaches[0].Label != "CN ×2" || v.MetadataCaches[0].Value != "2 GiB（环境默认，推算）" || v.MetadataCaches[1].Source != "TOML 文件＋代码默认值" || v.MetadataCaches[1].Value != "2 GiB（环境默认，推算）" || v.MetadataCaches[2].Value != "1 GiB" {
		t.Fatalf("metadata default or evidence source lost: %+v", v.MetadataCaches)
	}
	if len(v.Retrieval) != 2 || v.Retrieval[0].Label != "IVF" || len(v.Retrieval[0].Rows) != 2 || v.Retrieval[1].Rows[0].Value != "off" {
		t.Fatalf("missing variables became invented defaults: %+v", v.Retrieval)
	}
	if environmentSetting("0", true) != "0 B" || metadataCacheSetting("nil") != "未设置" {
		t.Fatal("metadata default handling changed unrelated cache zero or nil")
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("presentation changed archived zero or missing values")
	}
}

func TestEnvironmentCompactsIdenticalNodeConfiguration(t *testing.T) {
	e := newEnvironmentEvidence(environmentInputs{})
	for _, node := range []ReportedNode{{"CN", "a"}, {"CN", "b"}, {"CN", "c"}, {"LOG", "d"}, {"LOG", "e"}} {
		e.Server.ReportedNodes = append(e.Server.ReportedNodes, node)
		for _, field := range []struct{ key, value string }{
			{"name", "SHARED"}, {"backend", "S3"},
			{"cache.memorycapacity.bytesize", "536870912"},
			{"cache.diskcapacity.bytesize", "8589934592"},
		} {
			e.Server.ReportedConfig = append(e.Server.ReportedConfig, ReportedConfig{node.NodeType, node.NodeID, "commonconfig.fileservices[1].config." + field.key, field.value, field.value})
		}
	}
	r := Report{RunKind: "environment_inspection", Dataset: "inspection", Environment: e}
	before, _ := json.Marshal(r)
	v := buildEnvironmentPresentation(r)
	if len(v.Caches) != 2 || len(v.Nodes) != 5 || len(v.Configs) != 6 {
		t.Fatalf("unexpected compacted rows: caches=%d nodes=%d configs=%d", len(v.Caches), len(v.Nodes), len(v.Configs))
	}
	for i, want := range []struct {
		label string
		count int
	}{{"CN ×3", 3}, {"LOG ×2", 2}} {
		row := v.Caches[i]
		if row.Node != want.label || row.NodeCount != want.count || row.Memory != "512 MiB" || row.Disk != "8 GiB" {
			t.Fatalf("node count or per-node capacity lost: %+v", row)
		}
	}
	for _, row := range v.Configs {
		if row.Node != "CN ×3" && row.Node != "LOG ×2" {
			t.Fatalf("configuration detail still duplicates nodes: %+v", row)
		}
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("compaction changed raw node records")
	}
	// Values that round to the same display capacity must retain separate groups.
	for i := range e.Server.ReportedConfig {
		item := &e.Server.ReportedConfig[i]
		if item.NodeID == "c" && strings.HasSuffix(item.Name, "memorycapacity.bytesize") {
			item.CurrentValue = strconv.FormatInt((512<<20)+1, 10)
		}
	}
	v = buildEnvironmentPresentation(r)
	if len(v.Caches) != 3 || v.Caches[0].Node != "CN ×2" || v.Caches[1].Node != "CN 3" || v.Caches[0].Memory != v.Caches[1].Memory || len(v.Configs) != 7 {
		t.Fatalf("raw configuration difference was hidden: caches=%+v configs=%d", v.Caches, len(v.Configs))
	}
}

func TestEnvironmentReportKeepsConflictingEvidenceAndFiltersHistory(t *testing.T) {
	e := newEnvironmentEvidence(environmentInputs{})
	cpu, memory, fileCapacity := 8.0, int64(16<<30), int64(2<<30)
	e.Client.LogicalCPUs = 64
	e.Declared = &DeclaredEnvironment{Deployment: "docker", Scope: "container with CN and TN", CPULimit: &cpu, MemoryLimitBytes: &memory}
	e.Server.ReportedNodes = []ReportedNode{{"CN", "cn-a"}}
	e.Server.ReportedConfig = []ReportedConfig{
		{"CN", "cn-a", "commonconfig.fileservices[4].config.name", "SHARED", "SHARED"},
		{"CN", "cn-a", "commonconfig.fileservices[4].config.backend", "DISK-V2", "DISK"},
		{"CN", "cn-a", "commonconfig.fileservices[4].config.cache.memorycapacity.bytesize", "1073741824", "536870912"},
		{"CN", "cn-a", "commonconfig.fileservices[4].config.cache.remotecacheenabled", "true", "false"},
		{"CN", "cn-a", "commonconfig.fileservices[0].config.name", "ETL", "LOCAL"},
		{"CN", "cn-a", "commonconfig.fileservices[0].config.cache.memorycapacity.bytesize", "123", "456"},
		{"CN", "cn-a", "config.frontend.iceberg.planningmaxmemory", "123", "456"},
	}
	e.Configs = []MOConfigEvidence{{ServiceType: "CN", NodeID: "cn-a", File: fileRef{Path: "cn.toml", SHA256: "original-file-digest"}, FileServices: []FileServiceConfig{
		{Name: "SHARED", Backend: "S3", MemoryCapacity: ConfiguredMemory{Status: "configured", Bytes: &fileCapacity}, RemoteCache: new(bool)},
		{Name: "TMP", Backend: "DISK-TMP"},
	}, Memory: []ConfiguredMemory{{Key: "cn.frontend.iceberg.planning-max-memory", Status: "configured", Bytes: &fileCapacity}}}}
	r := Report{RunKind: "environment_inspection", Dataset: "inspection", Status: "passed", Environment: e}
	before, _ := json.Marshal(r)
	v := buildEnvironmentPresentation(r)
	if len(v.Caches) != 1 || v.Caches[0].Memory != "1 GiB" || v.Caches[0].Backend != "DISK-V2" {
		t.Fatal("file values overwrote service-reported values")
	}
	found := false
	for _, row := range v.Configs {
		if row.Parameter == "SHARED · 内存缓存容量" {
			found = row.Snapshot == "1 GiB" && row.Default == "512 MiB" && row.File == "cn.toml: 2 GiB"
		}
	}
	if !found {
		t.Fatal("conflicting file value or default was hidden")
	}
	found = false
	for _, row := range v.Summary {
		if row.Label == "资源配额" {
			found = row.Value == "8 CPU / 16 GiB"
		}
	}
	if !found {
		t.Fatal("client CPU count became server quota")
	}
	var html bytes.Buffer
	if err := renderReportHTML(&html, r); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"iceberg", "etl", "tmp", "remotecacheenabled", "远端开关", "远端缓存", "ZgotmplZ"} {
		if strings.Contains(strings.ToLower(html.String()), strings.ToLower(forbidden)) {
			t.Fatalf("historical report displays ignored field: %s", forbidden)
		}
	}
	for _, required := range []string{"版本与部署", "节点与缓存", "检索参数", "资源监控", "每节点磁盘缓存", "上报时间未知", "诊断详情", "与文件显式值不同", "cn.toml: 2 GiB"} {
		if !strings.Contains(html.String(), required) {
			t.Fatalf("missing report evidence: %s", required)
		}
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("render changed historical raw evidence")
	}
}
