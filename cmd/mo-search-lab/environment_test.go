// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEnvironmentSQLDiscoveryAndPermissionFallback(t *testing.T) {
	for _, forbidden := range []bool{false, true} {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		mock.ExpectQuery(regexp.QuoteMeta(environmentBuildSQL)).WillReturnRows(sqlmock.NewRows([]string{"comment", "build", "git"}).AddRow("MatrixOne", "2026-08-26", "abc123"))
		mock.ExpectQuery(regexp.QuoteMeta(environmentVariablesSQL)).WillReturnRows(sqlmock.NewRows([]string{"Variable_name", "Value"}).AddRow("probe_limit", "5").AddRow("kmeans_train_percent", "10").AddRow("hnsw_threads_search", "0").AddRow("enable_vector_prefilter_by_default", "off").AddRow("version_compile_os", ""))
		if forbidden {
			mock.ExpectQuery(regexp.QuoteMeta(environmentTopologySQL)).WillReturnError(errors.New("permission denied"))
		} else {
			mock.ExpectQuery(regexp.QuoteMeta(environmentTopologySQL)).WillReturnRows(sqlmock.NewRows([]string{"UUID", "Address", "Work State", "Labels"}).AddRow("cn-a", "cn-a:6001", "Working", "account:tenant;").AddRow("cn-b", "cn-b:6001", "Working", ""))
		}
		if forbidden {
			mock.ExpectQuery(regexp.QuoteMeta(environmentReportedNodesSQL)).WillReturnError(errors.New("permission denied"))
			mock.ExpectQuery(regexp.QuoteMeta(environmentReportedConfigSQL)).WillReturnError(errors.New("view is unavailable"))
		} else {
			mock.ExpectQuery(regexp.QuoteMeta(environmentReportedNodesSQL)).WillReturnRows(sqlmock.NewRows([]string{"node_type", "node_id"}).AddRow("cn", "cn-a").AddRow("tn", "tn-a"))
			mock.ExpectQuery(regexp.QuoteMeta(environmentReportedConfigSQL)).WillReturnRows(sqlmock.NewRows([]string{"node_type", "node_id", "name", "current_value", "default_value"}).AddRow("cn", "cn-a", "commonconfig.fileservices[1].config.name", "SHARED", "SHARED").AddRow("cn", "cn-a", "commonconfig.fileservices[1].config.cache.memorycapacity.bytesize", "536870912", "536870912"))
		}
		e := newEnvironmentEvidence(environmentInputs{})
		collectSQLServerEnvironment(context.Background(), db, e, "8.0.30-MatrixOne-v4.2.1", time.Second)
		if e.Server.Version == "" || e.Server.GitCommit != "abc123" || e.Server.Variables["probe_limit"] != "5" || e.Server.Variables["kmeans_train_percent"] != "10" || e.Server.Variables["hnsw_threads_search"] != "0" || e.Server.Variables["enable_vector_prefilter_by_default"] != "off" || len(e.Server.Probes) != 5 {
			t.Fatalf("missing evidence: %+v", e.Server)
		}
		if forbidden {
			if e.Server.VisibleCNCount != nil || e.Server.Probes[2].Status != "unavailable" || len(e.Server.ReportedNodes) != 0 || len(e.Server.ReportedConfig) != 0 || e.Server.Probes[3].Status != "unavailable" || e.Server.Probes[4].Status != "unavailable" {
				t.Fatal("permission failure became zero CNs")
			}
		} else if e.Server.VisibleCNCount == nil || *e.Server.VisibleCNCount != 2 || e.Server.VisibleCNs[1].ID != "cn-b" {
			t.Fatal("visible CN evidence lost")
		} else if len(e.Server.ReportedNodes) != 2 || len(e.Server.ReportedConfig) != 2 {
			t.Fatal("service-reported configuration lost")
		}
		rows := buildEnvironmentPresentation(Report{Environment: e}).Summary
		for _, row := range rows {
			if row.Label == "部署与资源配额" && row.Value != "未提供" {
				t.Fatal("client became server hardware")
			}
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
}

func TestMOConfigCachesUnitsOmissionsAndSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cn.toml")
	body := `service-type = "CN"
[metacache]
memory-capacity = 0
[cn]
uuid = "cn-a"
[cn.Pipeline]
guest-size = 67108864
[cn.frontend.iceberg]
manifest-cache-bytes = "1GB"
[[fileservice]]
name = "LOCAL"
backend = "DISK"
[[fileservice]]
name = "SHARED"
backend = "S3"
key-secret = "never-export-this"
[fileservice.cache]
memory-capacity = "512MB"
disk-capacity = "1.5GiB"
remote-cache-enabled = false
disk-path = "/cache/shared"
[[fileservice]]
name = "USER"
[fileservice.cache]
memory-capacity = 0
disk-capacity = "invalid-size"
[[fileservice]]
name = "ETL"
[fileservice.cache]
memory-capacity = "2GB"
[[fileservice]]
name = "tmp"
[fileservice.cache]
memory-capacity = "3GB"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := readMOConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.ServiceType != "CN" || config.NodeID != "cn-a" || len(config.FileServices) != 3 || len(config.Memory) != 2 || config.File.SHA256 == "" {
		t.Fatalf("incomplete TOML: %+v", config)
	}
	if config.Memory[0].Key != "metacache.memory-capacity" || config.Memory[0].Bytes == nil || *config.Memory[0].Bytes != 0 {
		t.Fatal("explicit metadata default was lost")
	}
	local, shared, custom := config.FileServices[0], config.FileServices[1], config.FileServices[2]
	if local.MemoryCapacity.Status != "not_set" || local.MemoryCapacity.Bytes != nil {
		t.Fatal("missing cache capacity became a default")
	}
	if shared.MemoryCapacity.Bytes == nil || *shared.MemoryCapacity.Bytes != 512<<20 || shared.DiskCapacity.Bytes == nil || *shared.DiskCapacity.Bytes != 1536<<20 || shared.RemoteCache != nil {
		t.Fatalf("wrong capacities: %+v", shared)
	}
	if custom.MemoryCapacity.Bytes == nil || *custom.MemoryCapacity.Bytes != 0 || custom.DiskCapacity.Status != "unrecognized" {
		t.Fatal("zero and unknown values confused")
	}
	encoded, _ := json.Marshal(config)
	if bytes.Contains(encoded, []byte("never-export-this")) || bytes.Contains(encoded, []byte("key-secret")) {
		t.Fatal("storage credential leaked")
	}
	for _, excluded := range []string{"ETL", "tmp", "iceberg", "remote_cache_enabled"} {
		if bytes.Contains(encoded, []byte(excluded)) {
			t.Fatalf("irrelevant config retained: %s", excluded)
		}
	}
	for raw, want := range map[string]int64{"8GB": 8 << 30, "1KiB": 1024, "2.5 MB": 2621440, "0": 0, "1b": 1} {
		if actual, ok := parseMOByteSize(raw); !ok || actual != want {
			t.Fatalf("%s=%d/%v, want %d", raw, actual, ok, want)
		}
	}
	for _, raw := range []string{"-1GB", "true", "10percent", "999999EB", "NaN"} {
		if _, ok := parseMOByteSize(raw); ok {
			t.Fatalf("accepted invalid size %q", raw)
		}
	}
	if err := os.WriteFile(path, []byte("password = 'secret-unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readMOConfig(path)
	if err == nil || strings.Contains(err.Error(), "secret-unclosed") {
		t.Fatal("parser failure leaked TOML source")
	}
}

func TestEnvironmentInputAdmissionAndReportCompatibility(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "declared.json")
	valid := `{"deployment":"kubernetes","scope":"CN pods in namespace example","cpu_limit":8,"memory_limit_bytes":17179869184,"cn_count":2,"tn_count":1}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	inputs, err := loadEnvironmentInputs(options{environmentFile: path})
	if err != nil || inputs.Declared.CNCount == nil || *inputs.Declared.CNCount != 2 {
		t.Fatalf("declared input: %+v %v", inputs, err)
	}
	for _, body := range []string{`{"cpu_limit":8}`, `{"deployment":"guessed"}`, `{"password":"no"}`, `null`, valid + `{}`} {
		os.WriteFile(path, []byte(body), 0o600)
		if _, err := loadEnvironmentInputs(options{environmentFile: path}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	badConfig := filepath.Join(dir, "bad.toml")
	os.WriteFile(badConfig, []byte("[invalid"), 0o600)
	report, err := inspectEnvironment(context.Background(), options{host: "127.0.0.1", port: 1, timeout: time.Second, moConfigs: configPaths{badConfig}})
	if err == nil || report.Dataset != "" || !strings.Contains(err.Error(), "invalid TOML") {
		t.Fatal("invalid input reached SQL inspection")
	}
	r := Report{RunKind: "environment_inspection", Dataset: "inspection", Status: "passed", ToolVersion: "test", StartedAt: time.Now(), Environment: newEnvironmentEvidence(inputs)}
	r.Environment.Server.Version = "MO-test"
	r.Environment.Declared.Notes = "<script>escape me</script>"
	r.Environment.Declared.CPUModel = "<script>escape me</script>"
	out := filepath.Join(dir, "report")
	if err := writeReport(out, r); err != nil {
		t.Fatal(err)
	}
	header, err := readTerminalHeader(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := loadTerminalDocument(header)
	if err != nil || len(doc.View.EnvironmentView.Summary) == 0 || len(doc.View.Scenarios) != 0 {
		t.Fatalf("inspection cannot be read in CLI: %v", err)
	}
	model := &terminalModel{doc: doc, datasets: []terminalDataset{{ID: header.Dataset, Runs: []terminalRun{header}}}, width: 100, height: 32}
	for section := range terminalSections {
		model.section = section
		if err := model.writePlain(new(bytes.Buffer)); err != nil {
			t.Fatalf("inspection page %d failed: %v", section, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(out, "report.json"))
	sidecar, _ := os.ReadFile(filepath.Join(out, "environment.json"))
	var saved Report
	json.Unmarshal(raw, &saved)
	var e EnvironmentEvidence
	json.Unmarshal(sidecar, &e)
	if e.Server.Version != saved.Environment.Server.Version {
		t.Fatal("sidecar differs from report")
	}
	if err := renderSavedReport(out); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(out, "report.json"))
	if !bytes.Equal(raw, after) {
		t.Fatal("render mutated raw environment")
	}
	html, _ := os.ReadFile(filepath.Join(out, "report.html"))
	for _, want := range []string{"SQL 可见 CN 数", "未知", "客户声明", "环境原始记录", "&lt;script&gt;"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(html), "<script>escape me") {
		t.Fatal("environment HTML injection")
	}
	legacy := Report{Dataset: "legacy", Status: "passed"}
	var body bytes.Buffer
	if err := renderReportHTML(&body, legacy); err != nil || !strings.Contains(body.String(), "此历史记录未自动采集") {
		t.Fatal("legacy report no longer renders")
	}
}
