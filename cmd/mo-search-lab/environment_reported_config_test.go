// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestReportedConfigAllowlistAndSourceSeparation(t *testing.T) {
	rows := [][]string{
		{"cn", "cn-a", "commonconfig.fileservices[1].config.name", "SHARED", "SHARED"},
		{"cn", "cn-a", "commonconfig.fileservices[1].config.backend", "S3", "DISK"},
		{"cn", "cn-a", "commonconfig.fileservices[1].config.cache.memorycapacity.bytesize", "1073741824", "536870912"},
		{"cn", "cn-a", "commonconfig.fileservices[1].config.cache.diskcapacity.bytesize", "0", "8589934592"},
		{"cn", "cn-a", "commonconfig.fileservices[1].config.cache.remotecacheenabled", "false", "false"},
		{"cn", "cn-a", "commonconfig.fileservices[1].config.cache.diskpath.string", "/cache/shared", "mo-data/shared-cache"},
		{"cn", "cn-a", "commonconfig.fileservices[1].config.s3.keysecret", "never-export-this", "secret-default"},
		{"cn", "cn-a", "commonconfig.fileservices[1].config.cache.memorycapacity.password", "never-export-this", ""},
		{"cn", "cn-a", "commonconfig.fileservices[64].config.cache.memorycapacity.bytesize", "4096", "0"},
		{"cn", "cn-a", "config.frontend.mempoolmaxsize", "1099511627776", "1099511627776"},
		{"cn", "cn-a", "commonconfig.fileservices[0].config.name", "ETL", "LOCAL"},
		{"cn", "cn-a", "commonconfig.fileservices[0].config.cache.memorycapacity.bytesize", "123", "456"},
		{"cn", "cn-a", "commonconfig.fileservices[3].config.name", "tmp", "TMP"},
		{"cn", "cn-a", "commonconfig.fileservices[3].config.cache.diskcapacity.bytesize", "123", "456"},
		{"cn", "cn-a", "config.frontend.iceberg.manifestcachebytes", "123", "456"},
	}
	configs, err := parseReportedConfig(rows)
	if err != nil || len(configs) != 6 {
		t.Fatalf("allowlist: %d / %v", len(configs), err)
	}
	encoded, _ := json.Marshal(configs)
	if strings.Contains(string(encoded), "never-export-this") || strings.Contains(string(encoded), "keysecret") {
		t.Fatal("configuration secret retained")
	}
	if strings.Contains(environmentReportedConfigSQL, "SELECT *") || strings.Contains(environmentReportedConfigSQL, "s3.") {
		t.Fatal("SQL does not restrict sensitive fields")
	}
	if strings.Contains(environmentReportedConfigSQL, "iceberg") || strings.Contains(environmentReportedConfigSQL, "remotecacheenabled") || strings.Contains(string(encoded), "remotecacheenabled") || !strings.Contains(environmentReportedConfigSQL, "NOT IN ('etl', 'tmp')") || !strings.Contains(environmentReportedConfigSQL, "fs.node_id = c.node_id") {
		t.Fatal("irrelevant configs are not excluded at the SQL boundary")
	}
	nodes, err := parseReportedNodes([][]string{{"cn", "cn-a"}, {"tn", "tn-a"}, {"cn", "cn-a"}})
	if err != nil || len(nodes) != 2 {
		t.Fatalf("reported nodes: %+v / %v", nodes, err)
	}
	e := newEnvironmentEvidence(environmentInputs{})
	e.Server.ReportedConfig, e.Server.ReportedNodes = configs, nodes
	e.Configs = []MOConfigEvidence{{ServiceType: "CN", NodeID: "cn-a", FileServices: []FileServiceConfig{{Name: "SHARED", MemoryCapacity: ConfiguredMemory{Status: "not_set"}, DiskCapacity: ConfiguredMemory{Status: "not_set"}}}}}
	v := buildEnvironmentPresentation(Report{Environment: e})
	if len(v.Caches) != 1 || v.Caches[0].Service != "SHARED" || v.Caches[0].Memory != "1 GiB" || v.Caches[0].Disk != "0 B" {
		t.Fatalf("cache summary lost values: %+v", v.Caches)
	}
	found := false
	for _, row := range v.Configs {
		if row.Parameter == "SHARED · 内存缓存容量" {
			found = row.Snapshot == "1 GiB" && row.Default == "512 MiB" && row.File == "未显式设置"
		}
	}
	if !found || e.Configs[0].FileServices[0].MemoryCapacity.Status != "not_set" {
		t.Fatal("TOML omissions not kept separately from service-reported values")
	}
	body, _ := json.Marshal(v)
	for _, irrelevant := range []string{"iceberg", "ETL", "tmp", "remotecacheenabled", "远端开关"} {
		if strings.Contains(string(body), irrelevant) {
			t.Fatalf("historical report displays ignored config: %s", irrelevant)
		}
	}
}

func TestReportedConfigMissingMalformedAndBounded(t *testing.T) {
	for _, rows := range [][][]string{
		nil,
		{{"cn", "cn-a", "other", "secret", "secret"}},
		{{"cn", "cn-a", "config.pipeline.hostsize"}},
		{{"", "cn-a", "config.pipeline.hostsize", "0", "0"}},
		{{"cn", "cn-a", "config.pipeline.hostsize", "0", "0"}, {"cn", "cn-a", "config.pipeline.hostsize", "1", "0"}},
	} {
		if _, err := parseReportedConfig(rows); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("missing/malformed configuration accepted or echoed: %v", err)
		}
	}
	if _, err := parseReportedNodes(nil); err == nil {
		t.Fatal("empty node report treated as zero nodes")
	}
	if got := environmentSetting("nil", true); got != "未设置" {
		t.Fatal("nil capacity became zero")
	}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result := sqlmock.NewRows([]string{"node_type", "node_id", "name", "current_value", "default_value"})
	for i := 0; i < 2049; i++ {
		result.AddRow("cn", "cn-a", "config.pipeline.hostsize", "0", "0")
	}
	mock.ExpectQuery(regexp.QuoteMeta(environmentReportedConfigSQL)).WillReturnRows(result).RowsWillBeClosed()
	if _, err := readEnvironmentRowsWithLimit(context.Background(), db, environmentReportedConfigSQL, 2048); err == nil || !strings.Contains(err.Error(), "2048 rows") {
		t.Fatal("configuration response limit not enforced")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
