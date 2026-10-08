// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type MonitoringConfig struct {
	Provider      string        `json:"provider"`
	URL           string        `json:"url"`
	DatasourceUID string        `json:"datasource_uid,omitempty"`
	TokenEnv      string        `json:"token_env,omitempty"`
	Scope         string        `json:"scope"`
	Step          string        `json:"step,omitempty"`
	Timeout       string        `json:"timeout,omitempty"`
	Queries       []MetricQuery `json:"queries"`
}

type MetricQuery struct {
	Name  string `json:"name"`
	Query string `json:"query"`
	Unit  string `json:"unit"`
}

type MonitoringEvidence struct {
	Status        string           `json:"status"`
	Reason        string           `json:"reason,omitempty"`
	Provider      string           `json:"provider,omitempty"`
	URL           string           `json:"url,omitempty"`
	DatasourceUID string           `json:"datasource_uid,omitempty"`
	Scope         string           `json:"scope,omitempty"`
	WindowStart   *time.Time       `json:"window_start,omitempty"`
	WindowEnd     *time.Time       `json:"window_end,omitempty"`
	WindowMeaning string           `json:"window_meaning,omitempty"`
	StepSeconds   float64          `json:"step_seconds,omitempty"`
	Metrics       []MetricEvidence `json:"metrics,omitempty"`
}

type MetricEvidence struct {
	MetricQuery
	Status             string         `json:"status"`
	Error              string         `json:"error,omitempty"`
	UnavailableSamples int            `json:"unavailable_samples,omitempty"`
	Series             []MetricSeries `json:"series,omitempty"`
}

type MetricSeries struct {
	Labels  map[string]string `json:"labels"`
	Samples []MetricSample    `json:"samples"`
}

type MetricSample struct {
	Timestamp float64  `json:"timestamp"`
	Value     *float64 `json:"value"`
}

var datasourceUIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (config *MonitoringConfig) validate() error {
	if config.Provider != "grafana" && config.Provider != "prometheus" {
		return fmt.Errorf("monitor provider must be grafana or prometheus")
	}
	address, err := url.Parse(config.URL)
	if err != nil || len(config.URL) > 4096 || address.Host == "" || (address.Scheme != "https" && address.Scheme != "http") || address.User != nil || address.RawQuery != "" || address.Fragment != "" {
		return fmt.Errorf("monitor URL must be an HTTP(S) base URL without credentials, query or fragment")
	}
	if strings.TrimSpace(config.Scope) == "" || len(config.Scope) > 4096 {
		return fmt.Errorf("monitoring requires an explicit target scope")
	}
	if config.DatasourceUID != "" && (config.Provider != "grafana" || !datasourceUIDPattern.MatchString(config.DatasourceUID)) {
		return fmt.Errorf("invalid Grafana datasource UID")
	}
	if config.TokenEnv == "" {
		config.TokenEnv = "MO_BENCH_MONITOR_TOKEN"
	}
	if !environmentNamePattern.MatchString(config.TokenEnv) {
		return fmt.Errorf("invalid monitoring token environment variable name")
	}
	if config.Step == "" {
		config.Step = "15s"
	}
	if config.Timeout == "" {
		config.Timeout = "5s"
	}
	step, stepErr := time.ParseDuration(config.Step)
	timeout, timeoutErr := time.ParseDuration(config.Timeout)
	if stepErr != nil || step < time.Second || step > time.Hour || timeoutErr != nil || timeout < time.Millisecond || timeout > 10*time.Second {
		return fmt.Errorf("monitor step must be 1s..1h and timeout 1ms..10s")
	}
	if len(config.Queries) < 1 || len(config.Queries) > 8 {
		return fmt.Errorf("monitoring requires 1..8 explicit metric queries")
	}
	names := make(map[string]bool)
	for _, query := range config.Queries {
		if strings.TrimSpace(query.Name) == "" || len(query.Name) > 128 || names[query.Name] || strings.TrimSpace(query.Query) == "" || len(query.Query) > 4096 {
			return fmt.Errorf("metric queries require unique names and nonempty PromQL")
		}
		names[query.Name] = true
		switch query.Unit {
		case "cores", "bytes", "percent", "ratio", "count", "seconds":
		default:
			return fmt.Errorf("unsupported metric unit")
		}
	}
	return nil
}

type monitoringClient struct {
	http    *http.Client
	token   string
	baseURL string
}

func (client monitoringClient) get(ctx context.Context, path string, params url.Values, target any) error {
	address := strings.TrimRight(client.baseURL, "/") + path
	if len(params) > 0 {
		address += "?" + params.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("invalid monitor request")
	}
	request.Header.Set("Accept", "application/json")
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("monitor request: %w", ctx.Err())
		}
		return fmt.Errorf("monitor HTTP request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("monitor HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil {
		return fmt.Errorf("read monitor response failed")
	}
	if len(body) > 4<<20 {
		return fmt.Errorf("monitor response exceeds 4 MiB")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("invalid monitor JSON response")
	}
	return nil
}

func (client monitoringClient) discoverDatasource(ctx context.Context) (string, error) {
	var rows []struct {
		UID       string `json:"uid"`
		Type      string `json:"type"`
		IsDefault bool   `json:"isDefault"`
	}
	if err := client.get(ctx, "/api/datasources", nil, &rows); err != nil {
		return "", err
	}
	var candidates, defaults []string
	for _, row := range rows {
		if row.Type != "prometheus" || !datasourceUIDPattern.MatchString(row.UID) {
			continue
		}
		candidates = append(candidates, row.UID)
		if row.IsDefault {
			defaults = append(defaults, row.UID)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(defaults) == 1 {
		return defaults[0], nil
	}
	return "", fmt.Errorf("Grafana Prometheus datasource is missing or ambiguous; specify datasource_uid")
}

func collectMonitoring(ctx context.Context, config MonitoringConfig, start, end time.Time) MonitoringEvidence {
	result := MonitoringEvidence{Status: "unavailable", Provider: config.Provider, URL: config.URL, DatasourceUID: config.DatasourceUID, Scope: config.Scope, WindowStart: &start, WindowEnd: &end, WindowMeaning: "query phase, including plans and warmups; collected after measurement"}
	if end.Before(start) {
		result.Reason = "invalid measurement window"
		return result
	}
	step, _ := time.ParseDuration(config.Step)
	timeout, _ := time.ParseDuration(config.Timeout)
	if step <= 0 || timeout <= 0 {
		result.Reason = "unvalidated monitoring configuration"
		return result
	}
	// At most 1000 evaluation timestamps per series, including endpoints.
	step = max(step, time.Duration(math.Ceil(end.Sub(start).Seconds()/999))*time.Second)
	result.StepSeconds = step.Seconds()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := monitoringClient{http: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, token: os.Getenv(config.TokenEnv), baseURL: config.URL}
	if config.Provider == "grafana" && result.DatasourceUID == "" {
		uid, err := client.discoverDatasource(ctx)
		if err != nil {
			result.Reason = err.Error()
			return result
		}
		result.DatasourceUID = uid
	}
	path := "/api/v1/query_range"
	if config.Provider == "grafana" {
		path = "/api/datasources/proxy/uid/" + result.DatasourceUID + path
	}
	totalSamples, available, incomplete := 0, 0, false
	for _, query := range config.Queries {
		metric := MetricEvidence{MetricQuery: query, Status: "unavailable"}
		params := url.Values{"query": {query.Query}, "start": {strconv.FormatFloat(float64(start.UnixNano())/1e9, 'f', 6, 64)}, "end": {strconv.FormatFloat(float64(end.UnixNano())/1e9, 'f', 6, 64)}, "step": {strconv.FormatFloat(step.Seconds(), 'f', -1, 64)}, "timeout": {config.Timeout}}
		var response struct {
			Status string `json:"status"`
			Data   struct {
				ResultType string `json:"resultType"`
				Result     []struct {
					Metric map[string]string   `json:"metric"`
					Values [][]json.RawMessage `json:"values"`
				} `json:"result"`
			} `json:"data"`
			Warnings []string `json:"warnings"`
		}
		err := client.get(ctx, path, params, &response)
		if err == nil && (response.Status != "success" || response.Data.ResultType != "matrix") {
			err = fmt.Errorf("Prometheus range query did not return a successful matrix")
		}
		if err == nil && len(response.Data.Result) > 16 {
			err = fmt.Errorf("metric query exceeds 16 series; narrow its scope")
		}
		if err == nil {
			valid := 0
			for _, series := range response.Data.Result {
				if len(series.Values) > 1000 || len(series.Metric) > 32 {
					err = fmt.Errorf("metric series exceeds sample or label limit")
					break
				}
				for key, value := range series.Metric {
					if len(key) > 256 || len(value) > 4096 {
						err = fmt.Errorf("metric label exceeds size limit")
						break
					}
				}
				if err != nil {
					break
				}
				parsed := MetricSeries{Labels: series.Metric}
				for _, pair := range series.Values {
					if len(pair) != 2 {
						err = fmt.Errorf("invalid monitor sample")
						break
					}
					var timestamp float64
					var raw string
					if json.Unmarshal(pair[0], &timestamp) != nil || json.Unmarshal(pair[1], &raw) != nil || math.IsNaN(timestamp) || math.IsInf(timestamp, 0) || timestamp < float64(start.UnixNano())/1e9-0.001 || timestamp > float64(end.UnixNano())/1e9+0.001 {
						err = fmt.Errorf("invalid or out-of-window monitor sample")
						break
					}
					if len(parsed.Samples) > 0 && timestamp <= parsed.Samples[len(parsed.Samples)-1].Timestamp {
						err = fmt.Errorf("monitor sample timestamps are not increasing")
						break
					}
					point := MetricSample{Timestamp: timestamp}
					value, parseErr := strconv.ParseFloat(raw, 64)
					if parseErr != nil {
						err = fmt.Errorf("invalid metric sample value")
						break
					}
					if math.IsNaN(value) || math.IsInf(value, 0) {
						metric.UnavailableSamples++
					} else {
						point.Value = &value
						valid++
					}
					parsed.Samples = append(parsed.Samples, point)
				}
				if err != nil {
					break
				}
				totalSamples += len(parsed.Samples)
				if totalSamples > 64000 {
					err = fmt.Errorf("monitoring exceeds 64000 total samples")
					break
				}
				metric.Series = append(metric.Series, parsed)
			}
			if err == nil {
				if valid > 0 {
					available++
					metric.Status = "collected"
					if metric.UnavailableSamples > 0 || len(response.Warnings) > 0 {
						metric.Status = "partial"
						incomplete = true
					}
				} else {
					metric.Error = "no finite samples for the measured window"
					incomplete = true
				}
			}
		}
		if err != nil {
			metric.Series, metric.Error = nil, err.Error()
			incomplete = true
		}
		result.Metrics = append(result.Metrics, metric)
	}
	if available > 0 {
		result.Status = "collected"
		if incomplete {
			result.Status = "partial"
		}
	} else {
		result.Reason = "no metric query produced finite samples"
	}
	return result
}
