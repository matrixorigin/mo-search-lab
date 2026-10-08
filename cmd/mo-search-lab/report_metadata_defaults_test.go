// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"strings"
	"testing"
)

func TestMetadataDefaultCapacityBoundariesAndNodeScope(t *testing.T) {
	for _, pair := range [][2]int64{{1 << 30, 256 << 20}, {(2 << 30) - 1, ((2 << 30) - 1) / 4}, {2 << 30, 512 << 20}, {(16 << 30) - 1, 512 << 20}, {16 << 30, 1 << 30}, {(32 << 30) - 1, 1 << 30}, {32 << 30, 2 << 30}, {128 << 30, 2 << 30}} {
		capacity, _ := metadataDefaultCapacity(pair[0])
		if capacity != pair[1] {
			t.Fatalf("total %d: got %d want %d", pair[0], capacity, pair[1])
		}
	}
	e := newEnvironmentEvidence(environmentInputs{})
	e.Server.GitCommit = "d2393868a"
	limit, host, logHost := int64(8<<30), int64(64<<30), int64(8<<30)
	e.Client.LogicalCPUs = 128
	e.Declared = &DeclaredEnvironment{MemoryLimitBytes: &limit, Nodes: []DeclaredNode{{Role: "CN", ID: "a", SystemMemoryTotalBytes: &host}, {Role: "CN", ID: "b", SystemMemoryTotalBytes: &logHost}}}
	for _, id := range []string{"a", "b", "c"} {
		e.Server.ReportedConfig = append(e.Server.ReportedConfig, ReportedConfig{"CN", id, metadataCacheKey, "0", "0"})
	}
	v := buildEnvironmentPresentation(Report{Environment: e})
	if len(v.MetadataCaches) != 3 || v.MetadataCaches[0].Value != "2 GiB（环境默认，推算）" || v.MetadataCaches[1].Value != "512 MiB（环境默认，推算）" || v.MetadataCaches[2].Value != "默认容量未知" {
		t.Fatalf("node defaults were merged or container limit used: %+v", v.MetadataCaches)
	}
	e.Server.GitCommit = "unverified-build"
	v = buildEnvironmentPresentation(Report{Environment: e})
	if len(v.MetadataCaches) != 1 || v.MetadataCaches[0].Value != "默认容量未知" || !strings.Contains(v.MetadataCaches[0].Basis, "版本") {
		t.Fatalf("unknown build inherited verified defaults: %+v", v.MetadataCaches)
	}
}

func TestMetadataDefaultTOMLOmissionAndExplicitSQLOverride(t *testing.T) {
	e := newEnvironmentEvidence(environmentInputs{})
	e.Server.Version = "8.0.30-MatrixOne-v4.2.1"
	total := int64(32 << 30)
	e.Declared = &DeclaredEnvironment{Scope: "single MO host", SystemMemoryTotalBytes: &total}
	node := ReportedNode{"CN", "a"}
	row := metadataCachePresentation(e, node, ReportedConfig{}, false, []string{"not_set:"}, "cn.toml: 未显式设置")
	if row.Value != "2 GiB（环境默认，推算）" || row.Source != "TOML 文件＋代码默认值" {
		t.Fatalf("omitted TOML setting lost environmental default: %+v", row)
	}
	row = metadataCachePresentation(e, node, ReportedConfig{CurrentValue: "1073741824"}, true, []string{"0"}, "cn.toml: 0")
	if row.Value != "1 GiB" || row.Source != "SQL 启动快照" {
		t.Fatalf("explicit SQL configuration was replaced: %+v", row)
	}
	row = metadataCachePresentation(e, node, ReportedConfig{}, false, []string{"0", "1073741824"}, "cn.toml: 0\nother.toml: 1 GiB")
	if !strings.Contains(row.Basis, "差异") {
		t.Fatal("conflicting files became a single assumed default")
	}
}

func TestDeclaredSystemMemoryValidation(t *testing.T) {
	positive, zero := int64(32<<30), int64(0)
	for _, d := range []DeclaredEnvironment{
		{SystemMemoryTotalBytes: &positive},
		{Scope: "host", SystemMemoryTotalBytes: &zero},
		{SystemMemorySource: "source without measurement"},
		{Nodes: []DeclaredNode{{Role: "CN", ID: "a", SystemMemoryTotalBytes: &zero}}},
		{Nodes: []DeclaredNode{{Role: "TN", ID: "a"}, {Role: "DN", ID: "a"}}},
	} {
		if validateDeclaredEnvironment(d) == nil {
			t.Fatalf("invalid system-memory declaration accepted: %+v", d)
		}
	}
	if err := validateDeclaredEnvironment(DeclaredEnvironment{Nodes: []DeclaredNode{{Role: "CN", ID: "a", SystemMemoryTotalBytes: &positive, SystemMemorySource: "host /proc/meminfo"}}}); err != nil {
		t.Fatal(err)
	}
}
