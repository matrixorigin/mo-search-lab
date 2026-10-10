// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

type EnvironmentEvidence struct {
	SchemaVersion int                  `json:"schema_version"`
	ObservedAt    time.Time            `json:"observed_at"`
	Client        ClientEnvironment    `json:"client"`
	Server        ServerEnvironment    `json:"server"`
	Declared      *DeclaredEnvironment `json:"declared,omitempty"`
	DeclaredFile  *fileRef             `json:"declared_file,omitempty"`
	Configs       []MOConfigEvidence   `json:"configs,omitempty"`
	Monitoring    MonitoringEvidence   `json:"monitoring"`
}

type ClientEnvironment struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	GoVersion   string `json:"go_version"`
	Hostname    string `json:"hostname,omitempty"`
	LogicalCPUs int    `json:"logical_cpus"`
}

type ServerEnvironment struct {
	Version        string             `json:"version,omitempty"`
	VersionComment string             `json:"version_comment,omitempty"`
	BuildTime      string             `json:"build_time,omitempty"`
	GitCommit      string             `json:"git_commit,omitempty"`
	Variables      map[string]string  `json:"session_defaults,omitempty"`
	VisibleCNs     []VisibleCN        `json:"visible_cns,omitempty"`
	VisibleCNCount *int               `json:"visible_cn_count,omitempty"`
	TopologyScope  string             `json:"topology_scope"`
	ReportedNodes  []ReportedNode     `json:"reported_nodes,omitempty"`
	ReportedConfig []ReportedConfig   `json:"reported_config,omitempty"`
	Probes         []EnvironmentProbe `json:"probes,omitempty"`
}

type VisibleCN struct {
	ID      string `json:"id"`
	Address string `json:"address"`
	State   string `json:"state"`
	Labels  string `json:"labels,omitempty"`
}

type EnvironmentProbe struct {
	SQL    string `json:"sql"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type DeclaredEnvironment struct {
	Deployment             string         `json:"deployment,omitempty"`
	Scope                  string         `json:"scope,omitempty"`
	Host                   string         `json:"host,omitempty"`
	OS                     string         `json:"os,omitempty"`
	CPUModel               string         `json:"cpu_model,omitempty"`
	CPULimit               *float64       `json:"cpu_limit,omitempty"`
	MemoryLimitBytes       *int64         `json:"memory_limit_bytes,omitempty"`
	SystemMemoryTotalBytes *int64         `json:"system_memory_total_bytes,omitempty"`
	SystemMemorySource     string         `json:"system_memory_source,omitempty"`
	CNCount                *int           `json:"cn_count,omitempty"`
	TNCount                *int           `json:"tn_count,omitempty"`
	Nodes                  []DeclaredNode `json:"nodes,omitempty"`
	Notes                  string         `json:"notes,omitempty"`
}

type DeclaredNode struct {
	Role                   string   `json:"role"`
	ID                     string   `json:"id"`
	Address                string   `json:"address,omitempty"`
	CPULimit               *float64 `json:"cpu_limit,omitempty"`
	MemoryLimitBytes       *int64   `json:"memory_limit_bytes,omitempty"`
	SystemMemoryTotalBytes *int64   `json:"system_memory_total_bytes,omitempty"`
	SystemMemorySource     string   `json:"system_memory_source,omitempty"`
}

type environmentInputs struct {
	Declared *DeclaredEnvironment
	File     *fileRef
	Configs  []MOConfigEvidence
	Monitor  *MonitoringConfig
}

// Validate local input before creating any SQL connections or test objects.
func loadEnvironmentInputs(o options) (environmentInputs, error) {
	var inputs environmentInputs
	if o.environmentFile != "" {
		data, ref, err := readEvidenceFile(o.environmentFile)
		if err != nil {
			return inputs, err
		}
		var declared DeclaredEnvironment
		if err := decodeEvidenceJSON(data, &declared); err != nil {
			return inputs, fmt.Errorf("environment file: %w", err)
		}
		if err := validateDeclaredEnvironment(declared); err != nil {
			return inputs, err
		}
		inputs.Declared, inputs.File = &declared, &ref
	}
	if len(o.moConfigs) > 16 {
		return inputs, fmt.Errorf("at most 16 MO config files are supported")
	}
	for _, path := range o.moConfigs {
		config, err := readMOConfig(path)
		if err != nil {
			return inputs, err
		}
		inputs.Configs = append(inputs.Configs, config)
	}
	if o.monitoringConfig != "" {
		data, _, err := readEvidenceFile(o.monitoringConfig)
		if err != nil {
			return inputs, err
		}
		var monitor MonitoringConfig
		if err := decodeEvidenceJSON(data, &monitor); err != nil {
			return inputs, fmt.Errorf("monitoring config: %w", err)
		}
		if err := monitor.validate(); err != nil {
			return inputs, err
		}
		inputs.Monitor = &monitor
	}
	return inputs, nil
}

func newEnvironmentEvidence(inputs environmentInputs) *EnvironmentEvidence {
	host, _ := os.Hostname()
	return &EnvironmentEvidence{
		SchemaVersion: 1, ObservedAt: time.Now().UTC(),
		Client:   ClientEnvironment{runtime.GOOS, runtime.GOARCH, runtime.Version(), host, runtime.NumCPU()},
		Server:   ServerEnvironment{TopologyScope: "CNs visible to the connected SQL account; complete cluster topology is unknown"},
		Declared: inputs.Declared, DeclaredFile: inputs.File, Configs: inputs.Configs,
		Monitoring: MonitoringEvidence{Status: "unavailable", Reason: "monitoring is not configured"},
	}
}

func readEvidenceFile(path string) ([]byte, fileRef, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fileRef{}, fmt.Errorf("read evidence file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fileRef{}, err
	}
	if !info.Mode().IsRegular() {
		return nil, fileRef{}, fmt.Errorf("evidence file must be regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return nil, fileRef{}, err
	}
	if len(data) > 1<<20 {
		return nil, fileRef{}, fmt.Errorf("evidence file exceeds 1 MiB")
	}
	digest := sha256.Sum256(data)
	return data, fileRef{filepath.Base(path), hex.EncodeToString(digest[:])}, nil
}

func decodeEvidenceJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("expected JSON object")
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected a single JSON object")
	}
	return nil
}

func validateDeclaredEnvironment(d DeclaredEnvironment) error {
	if len(d.Nodes) > 256 {
		return fmt.Errorf("at most 256 declared nodes are supported")
	}
	for _, text := range []string{d.Deployment, d.Scope, d.Host, d.OS, d.CPUModel, d.Notes, d.SystemMemorySource} {
		if len(text) > 4096 {
			return fmt.Errorf("environment text exceeds 4096 bytes")
		}
	}
	if d.Deployment != "" && d.Deployment != "docker" && d.Deployment != "kubernetes" && d.Deployment != "bare-metal" && d.Deployment != "unknown" {
		return fmt.Errorf("deployment must be docker, kubernetes, bare-metal or unknown")
	}
	validResources := func(cpu *float64, memory *int64) bool {
		return (cpu == nil || !math.IsNaN(*cpu) && !math.IsInf(*cpu, 0) && *cpu > 0) && (memory == nil || *memory > 0)
	}
	validSystemMemory := func(total *int64, source string) bool {
		return (total == nil || *total > 0) && len(source) <= 4096 && (source == "" || total != nil)
	}
	if !validSystemMemory(d.SystemMemoryTotalBytes, d.SystemMemorySource) {
		return fmt.Errorf("system memory total must be positive; its source requires a total")
	}
	if !validResources(d.CPULimit, d.MemoryLimitBytes) || d.CNCount != nil && *d.CNCount < 0 || d.TNCount != nil && *d.TNCount < 0 {
		return fmt.Errorf("declared resource limits must be positive; node counts must be nonnegative")
	}
	if (d.CPULimit != nil || d.MemoryLimitBytes != nil || d.SystemMemoryTotalBytes != nil) && strings.TrimSpace(d.Scope) == "" {
		return fmt.Errorf("declared resource limits require scope")
	}
	seenNodes := make(map[ReportedNode]bool)
	for _, node := range d.Nodes {
		if node.ID == "" || !validResources(node.CPULimit, node.MemoryLimitBytes) {
			return fmt.Errorf("declared nodes require ID and valid resource limits")
		}
		if !validSystemMemory(node.SystemMemoryTotalBytes, node.SystemMemorySource) {
			return fmt.Errorf("invalid declared node system memory")
		}
		identity := ReportedNode{canonicalEnvironmentRole(node.Role), node.ID}
		if seenNodes[identity] {
			return fmt.Errorf("duplicate declared node")
		}
		seenNodes[identity] = true
		switch strings.ToUpper(node.Role) {
		case "CN", "TN", "DN", "LOG", "PROXY":
		default:
			return fmt.Errorf("invalid declared node role")
		}
	}
	return nil
}

const environmentBuildSQL = "SELECT @@version_comment, build_version(), git_version()"
const environmentVariablesSQL = "SHOW VARIABLES WHERE Variable_name IN ('version_compile_os', 'version_compile_machine', 'probe_limit', 'ivf_threads_build', 'ivf_threads_search', 'kmeans_train_percent', 'kmeans_max_iteration', 'hnsw_threads_build', 'hnsw_threads_search', 'hnsw_max_index_capacity', 'enable_vector_prefilter_by_default', 'enable_vector_auto_mode_by_default', 'ft_relevancy_algorithm', 'fulltext_bloom_filter_pushdown', 'connection_memory_limit', 'global_connection_memory_limit', 'max_allowed_packet')"
const environmentTopologySQL = "SHOW BACKEND SERVERS"

func collectSQLServerEnvironment(ctx context.Context, db sqlQueryer, evidence *EnvironmentEvidence, serverVersion string, timeout time.Duration) {
	evidence.Server.Version = serverVersion
	budget := min(timeout, 3*time.Second)
	if budget <= 0 {
		budget = 3 * time.Second
	}
	for _, statement := range []string{environmentBuildSQL, environmentVariablesSQL, environmentTopologySQL, environmentReportedNodesSQL, environmentReportedConfigSQL} {
		queryCtx, cancel := context.WithTimeout(ctx, budget)
		rowLimit := 256
		if statement == environmentReportedConfigSQL {
			rowLimit = 2048
		}
		rows, err := readEnvironmentRowsWithLimit(queryCtx, db, statement, rowLimit)
		cancel()
		probe := EnvironmentProbe{SQL: statement, Status: "collected"}
		if err != nil {
			probe.Status, probe.Error = "unavailable", err.Error()
			evidence.Server.Probes = append(evidence.Server.Probes, probe)
			continue
		}
		switch statement {
		case environmentBuildSQL:
			if len(rows) != 1 || len(rows[0]) != 3 {
				probe.Status, probe.Error = "unavailable", "unexpected build metadata columns"
			} else {
				evidence.Server.VersionComment, evidence.Server.BuildTime, evidence.Server.GitCommit = rows[0][0], rows[0][1], rows[0][2]
			}
		case environmentVariablesSQL:
			evidence.Server.Variables = make(map[string]string)
			for _, row := range rows {
				if len(row) != 2 {
					probe.Status, probe.Error = "unavailable", "unexpected variable columns"
					evidence.Server.Variables = nil
					break
				}
				evidence.Server.Variables[row[0]] = row[1]
			}
		case environmentTopologySQL:
			var nodes []VisibleCN
			for _, row := range rows {
				if len(row) != 4 {
					probe.Status, probe.Error = "unavailable", "unexpected CN columns"
					break
				}
				nodes = append(nodes, VisibleCN{row[0], row[1], row[2], row[3]})
			}
			if probe.Status == "collected" {
				count := len(nodes)
				evidence.Server.VisibleCNs, evidence.Server.VisibleCNCount = nodes, &count
			}
		case environmentReportedNodesSQL:
			nodes, err := parseReportedNodes(rows)
			if err != nil {
				probe.Status, probe.Error = "unavailable", err.Error()
			} else {
				evidence.Server.ReportedNodes = nodes
			}
		case environmentReportedConfigSQL:
			config, err := parseReportedConfig(rows)
			if err != nil {
				probe.Status, probe.Error = "unavailable", err.Error()
			} else {
				evidence.Server.ReportedConfig = config
			}
		}
		evidence.Server.Probes = append(evidence.Server.Probes, probe)
	}
}

func readEnvironmentRows(ctx context.Context, db sqlQueryer, statement string) ([][]string, error) {
	return readEnvironmentRowsWithLimit(ctx, db, statement, 256)
}

func readEnvironmentRowsWithLimit(ctx context.Context, db sqlQueryer, statement string, rowLimit int) ([][]string, error) {
	rows, err := db.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if len(columns) > 16 {
		return nil, fmt.Errorf("too many environment columns")
	}
	var result [][]string
	for rows.Next() {
		if len(result) >= rowLimit {
			return nil, fmt.Errorf("environment probe exceeds %d rows", rowLimit)
		}
		values := make([]sql.NullString, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := make([]string, len(values))
		for i, v := range values {
			if len(v.String) > 4096 {
				return nil, fmt.Errorf("environment value exceeds 4096 bytes")
			}
			row[i] = v.String
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func makeSQLConfig(o options) *mysql.Config {
	config := mysql.NewConfig()
	config.User, config.Passwd = o.user, o.password
	config.Net, config.Addr = "tcp", sqlAddress(o)
	config.Timeout, config.ReadTimeout, config.WriteTimeout = o.timeout, o.timeout*10, o.timeout*10
	config.AllowNativePasswords, config.InterpolateParams = true, true
	config.Params = map[string]string{"charset": "utf8mb4"}
	return config
}

func sqlAddress(o options) string { return net.JoinHostPort(o.host, fmt.Sprint(o.port)) }

func inspectEnvironment(ctx context.Context, o options) (report Report, err error) {
	notifyProgress(ctx, "读取环境配置文件")
	inputs, err := loadEnvironmentInputs(o)
	if err != nil {
		return report, err
	}
	report = Report{RunKind: "environment_inspection", Dataset: "environment-inspection", ToolVersion: version, StartedAt: time.Now().UTC(), Status: "failed", Cleanup: "not_applicable", ResourceMetrics: "unavailable", Environment: newEnvironmentEvidence(inputs), Profile: Profile{SQLAddress: sqlAddress(o), Timeout: o.timeout.String()}}
	defer func() {
		report.FinishedAt = time.Now().UTC()
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		} else {
			report.Status = "passed"
		}
	}()
	config := makeSQLConfig(o)
	notifyProgress(ctx, "连接 MO · "+sqlAddress(o))
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return report, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	queryCtx, cancel := context.WithTimeout(ctx, o.timeout)
	err = db.QueryRowContext(queryCtx, "SELECT VERSION()").Scan(&report.MatrixOneVersion)
	cancel()
	if err != nil {
		return report, fmt.Errorf("read MatrixOne version: %w", err)
	}
	notifyProgress(ctx, "采集版本、节点、缓存与检索参数")
	collectSQLServerEnvironment(ctx, db, report.Environment, report.MatrixOneVersion, o.timeout)
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if !strings.Contains(strings.ToLower(report.MatrixOneVersion), "matrixone") {
		return report, fmt.Errorf("SQL server does not identify itself as MatrixOne")
	}
	report.BinarySHA256, err = executableDigest()
	if inputs.Monitor != nil {
		report.Environment.Monitoring.Reason = "inspect does not run a measured workload; monitoring configuration is validated but no range was measured"
	}
	return report, err
}
