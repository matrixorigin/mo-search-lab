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

func TestReportTabsKeepEnvironmentWithItsOwnRun(t *testing.T) {
	e := newEnvironmentEvidence(environmentInputs{})
	e.Server.ReportedConfig = []ReportedConfig{{"CN", "cn1", "commonconfig.fileservices[0].config.name", "SHARED", "SHARED"}, {"CN", "cn1", "commonconfig.fileservices[0].config.cache.memorycapacity.bytesize", "536870912", "536870912"}}
	r := Report{Dataset: "tab-fixture", Status: "passed", MatrixOneVersion: "test-version", Environment: e}
	before, _ := json.Marshal(r)
	var body bytes.Buffer
	if err := renderReportHTML(&body, r); err != nil {
		t.Fatal(err)
	}
	page := body.String()
	_, panels, found := strings.Cut(page, `<section id="report-performance"`)
	if !found {
		t.Fatal("benchmark has no performance panel")
	}
	performance, environment, found := strings.Cut(panels, `<section id="report-environment"`)
	if !found || strings.Contains(performance, "每节点内存缓存") || !strings.Contains(environment, "每节点内存缓存") || !strings.Contains(environment, "512 MiB") {
		t.Fatal("environment was omitted or mixed into performance charts")
	}
	if strings.Contains(environment, `<summary>运行环境与检索配置</summary>`) || strings.Count(page, `role="tablist"`) != 1 {
		t.Fatal("environment tab still requires an extra disclosure")
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("tab layout changed original evidence")
	}
	r.RunKind = "environment_inspection"
	body.Reset()
	if err := renderReportHTML(&body, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.String(), `role="tablist"`) || !strings.Contains(body.String(), "节点与缓存") {
		t.Fatal("standalone inspect was turned into a performance report")
	}
	r.RunKind, r.Environment = "", nil
	body.Reset()
	if err := renderReportHTML(&body, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), "此历史记录未自动采集环境快照") {
		t.Fatal("historical report pretends it captured an environment")
	}
}
