// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvironmentDiagnosticsKeepDifferencesAndCollectionFailures(t *testing.T) {
	e := newEnvironmentEvidence(environmentInputs{})
	e.Server.GitCommit = "d2393868a"
	e.Server.VisibleCNs = []VisibleCN{{ID: "a", State: "Working"}, {ID: "b", State: "Draining", Address: "b:6001"}}
	e.Server.ReportedConfig = []ReportedConfig{
		{"CN", "a", "config.pipeline.hostsize", "0", "0"},
		{"CN", "a", metadataCacheKey, "0", "0"},
		{"CN", "a", "config.frontend.mempoolmaxsize", "536870912", "536870912"},
		{"CN", "b", "config.frontend.mempoolmaxsize", "1073741824", "536870912"},
	}
	file := int64((512 << 20) + 1)
	e.Configs = []MOConfigEvidence{{ServiceType: "CN", NodeID: "a", File: fileRef{Path: "cn.toml", SHA256: "keep-in-raw-only"}, Memory: []ConfiguredMemory{{Key: "cn.frontend.mempoolMaxSize", Bytes: &file, Status: "configured"}}}}
	e.Server.Probes = []EnvironmentProbe{{SQL: environmentReportedConfigSQL, Status: "collected"}, {SQL: environmentVariablesSQL, Status: "unavailable", Error: "permission denied"}}
	cpu, mem := 8.0, int64(16<<30)
	e.Declared = &DeclaredEnvironment{Nodes: []DeclaredNode{{Role: "CN", ID: "a", CPULimit: &cpu, MemoryLimitBytes: &mem}, {Role: "CN", ID: "b", CPULimit: &cpu, MemoryLimitBytes: &mem}}}
	r := Report{RunKind: "environment_inspection", Environment: e}
	before, _ := json.Marshal(r)
	v := buildEnvironmentPresentation(r)
	if len(v.Diagnostics.Configs) != 2 || len(v.Diagnostics.Nodes) != 1 || len(v.Diagnostics.Missing) != 1 || v.Diagnostics.Count != 4 || !v.Diagnostics.HasFileValues {
		t.Fatalf("diagnostics discarded useful evidence: %+v", v.Diagnostics)
	}
	if !strings.Contains(v.Diagnostics.NodeNotice, "1 个 CN 为 Draining") || !strings.Contains(v.Diagnostics.Configs[0].Difference, "536870913 B") {
		t.Fatalf("state or rounded file conflict hidden: %+v", v.Diagnostics)
	}
	found := false
	for _, row := range v.Summary {
		if row.Label == "CN ×2 资源配额" && row.Value == "8 CPU / 16 GiB（每节点）" {
			found = true
		}
	}
	if !found {
		t.Fatal("declared per-node quotas disappeared during simplification")
	}
	var body bytes.Buffer
	if err := renderReportHTML(&body, r); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"节点身份与 SQL 地址", "配置默认值、内存限制与 TOML 显式值", "MO TOML 文件与校验和", "客户端运行环境", "采集范围、补充信息与缺失原因", "检查身份与原始记录", "keep-in-raw-only", "Pipeline host-size"} {
		if strings.Contains(body.String(), removed) {
			t.Fatalf("report retained redundant detail: %s", removed)
		}
	}
	if strings.Count(body.String(), "<summary>诊断详情") != 1 || !strings.Contains(body.String(), "permission denied") || v.ClientSummary != "" {
		t.Fatal("consolidated disclosure, collection gap or inspect-only client visibility is wrong")
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("simplification modified original evidence")
	}
	r.RunKind = "benchmark"
	if buildEnvironmentPresentation(r).ClientSummary == "" {
		t.Fatal("benchmark lost its load-generator identity")
	}
}
