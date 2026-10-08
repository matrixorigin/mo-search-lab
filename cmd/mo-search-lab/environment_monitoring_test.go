// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func monitoringFixtureConfig(url string) MonitoringConfig {
	return MonitoringConfig{Provider: "prometheus", URL: url, Scope: "fixture-cn-only", Queries: []MetricQuery{{"CN CPU", `sum(rate(process_cpu_seconds_total{instance="fixture-cn"}[1m]))`, "cores"}}}
}

func TestMonitoringGrafanaDiscoveryWindowAuthAndNonfiniteSamples(t *testing.T) {
	start := time.Unix(1000, 0)
	end := start.Add(30 * time.Second)
	t.Setenv("BENCH_TEST_MONITOR_TOKEN", "never-store-token")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer never-store-token" {
			t.Error("missing auth")
		}
		if r.URL.Path == "/grafana/api/datasources" {
			fmt.Fprint(w, `[{"uid":"other","type":"loki","isDefault":true},{"uid":"prom-a","type":"prometheus","isDefault":false},{"uid":"prom-b","type":"prometheus","isDefault":true}]`)
			return
		}
		if r.URL.Path != "/grafana/api/datasources/proxy/uid/prom-b/api/v1/query_range" || r.URL.Query().Get("start") != "1000.000000" || r.URL.Query().Get("end") != "1030.000000" || r.URL.Query().Get("step") != "15" || r.URL.Query().Get("query") == "" {
			t.Errorf("bad range request: %s", r.URL)
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"pod":"cn-a"},"values":[[1000,"0.5"],[1015,"NaN"],[1030,"1.5"]]}]}}`)
	}))
	defer server.Close()
	config := monitoringFixtureConfig(server.URL + "/grafana")
	config.Provider = "grafana"
	config.TokenEnv = "BENCH_TEST_MONITOR_TOKEN"
	if err := config.validate(); err != nil {
		t.Fatal(err)
	}
	got := collectMonitoring(context.Background(), config, start, end)
	if got.Status != "partial" || got.DatasourceUID != "prom-b" || len(got.Metrics) != 1 || got.Metrics[0].UnavailableSamples != 1 || got.Metrics[0].Series[0].Samples[1].Value != nil || requests.Load() != 2 {
		t.Fatalf("invalid result: %+v", got)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "never-store-token") {
		t.Fatal("token persisted")
	}
	e := newEnvironmentEvidence(environmentInputs{})
	e.Monitoring = got
	plots := buildResourcePlots(e)
	if len(plots) != 1 || len(plots[0].Lines) != 1 || strings.Count(plots[0].Lines[0].Path, "M") != 2 || strings.Contains(plots[0].Lines[0].Path, "L") || plots[0].Lines[0].Samples != 2 || plots[0].Lines[0].Mean != 1 {
		t.Fatalf("missing sample became zero/connected path: %+v", plots)
	}
}

func TestMonitoringFailureEmptyLimitAndAdaptiveStep(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		reason     string
	}{
		{"empty", `{"status":"success","data":{"resultType":"matrix","result":[]}}`, 200, "no finite samples"},
		{"all NaN", `{"status":"success","data":{"resultType":"matrix","result":[{"values":[[1000,"NaN"]]}]}}`, 200, "no finite samples"},
		{"forbidden", "", 403, "HTTP status 403"},
		{"HTML", "<html>login</html>", 200, "invalid monitor JSON"},
		{"error", `{"status":"error","error":"do-not-echo-response"}`, 200, "successful matrix"},
		{"bad window", `{"status":"success","data":{"resultType":"matrix","result":[{"values":[[999,"1"]]}]}}`, 200, "out-of-window"},
		{"non-numeric", `{"status":"success","data":{"resultType":"matrix","result":[{"values":[[1000,"bad"]]}]}}`, 200, "invalid metric sample value"},
		{"oversized", strings.Repeat(" ", 4<<20) + "x", 200, "exceeds 4 MiB"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(test.status); fmt.Fprint(w, test.body) }))
			defer server.Close()
			config := monitoringFixtureConfig(server.URL)
			config.validate()
			got := collectMonitoring(context.Background(), config, time.Unix(1000, 0), time.Unix(1030, 0))
			if got.Status != "unavailable" || len(got.Metrics) != 1 || !strings.Contains(got.Metrics[0].Error, test.reason) {
				t.Fatalf("failure not reported: %+v", got)
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "do-not-echo-response") {
				t.Fatal("response error echoed")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step, _ := strconv.ParseFloat(r.URL.Query().Get("step"), 64)
		if step < 87 {
			t.Errorf("long run resolution not bounded: %g", step)
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
	}))
	defer server.Close()
	config := monitoringFixtureConfig(server.URL)
	config.validate()
	collectMonitoring(context.Background(), config, time.Unix(1000, 0), time.Unix(1000+86400, 0))
}

func TestMonitoringAmbiguityTimeoutRedirectAndConfigAdmission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/datasources" {
			fmt.Fprint(w, `[{"uid":"one","type":"prometheus"},{"uid":"two","type":"prometheus"}]`)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	config := monitoringFixtureConfig(server.URL)
	config.Provider = "grafana"
	config.Timeout = "20ms"
	config.validate()
	got := collectMonitoring(context.Background(), config, time.Unix(1000, 0), time.Unix(1030, 0))
	if !strings.Contains(got.Reason, "ambiguous") {
		t.Fatal("ambiguous datasource selected")
	}
	config.DatasourceUID = "explicit"
	got = collectMonitoring(context.Background(), config, time.Unix(1000, 0), time.Unix(1030, 0))
	if got.Status != "unavailable" || !strings.Contains(got.Metrics[0].Error, "request failed") {
		t.Fatalf("timeout not recorded: %+v", got)
	}
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, 302) }))
	defer redirect.Close()
	config = monitoringFixtureConfig(redirect.URL)
	config.validate()
	got = collectMonitoring(context.Background(), config, time.Unix(1000, 0), time.Unix(1030, 0))
	if leaked.Load() || !strings.Contains(got.Metrics[0].Error, "302") {
		t.Fatal("monitor followed redirect")
	}
	for _, mutate := range []func(*MonitoringConfig){func(c *MonitoringConfig) { c.URL = "https://user:secret@example.com" }, func(c *MonitoringConfig) { c.Scope = "" }, func(c *MonitoringConfig) { c.Queries = nil }, func(c *MonitoringConfig) { c.Step = "0s" }, func(c *MonitoringConfig) { c.Queries[0].Unit = "unknown" }, func(c *MonitoringConfig) { c.TokenEnv = "bad-name" }} {
		c := monitoringFixtureConfig("https://monitor.example")
		mutate(&c)
		if c.validate() == nil {
			t.Fatal("invalid monitor config accepted")
		}
	}
}
