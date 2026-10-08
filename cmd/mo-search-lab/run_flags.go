// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

type configPaths []string

func (paths *configPaths) String() string { return strings.Join(*paths, ", ") }
func (paths *configPaths) Set(path string) error {
	if strings.TrimSpace(path) == "" || len(*paths) >= 16 {
		return fmt.Errorf("--mo-config requires a path and supports at most 16 files")
	}
	*paths = append(*paths, path)
	return nil
}

func registerConnectionFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.host, "host", "127.0.0.1", "MatrixOne SQL host")
	fs.IntVar(&o.port, "port", 6001, "MatrixOne SQL port")
	fs.StringVar(&o.user, "user", "root", "MatrixOne SQL user")
	fs.StringVar(&o.passwordEnv, "password-env", "MO_BENCH_PASSWORD", "environment variable holding the SQL password")
	fs.DurationVar(&o.timeout, "timeout", 3*time.Minute, "query/plan timeout; data preparation allows 10x this budget")
}

func registerEnvironmentFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.environmentFile, "environment-file", "", "local JSON describing declared MO deployment/resources")
	fs.Var(&o.moConfigs, "mo-config", "local MO TOML file; repeat for multiple nodes; explicit values only")
	fs.StringVar(&o.monitoringConfig, "monitoring-config", "", "local JSON configuring optional scoped Grafana/Prometheus range queries")
}

func registerRunFlags(fs *flag.FlagSet, o *options) (levels, mixed, endpoints *string) {
	fs.StringVar(&o.pack, "pack", "", "path to a versioned pack directory")
	registerConnectionFlags(fs, o)
	registerEnvironmentFlags(fs, o)
	fs.StringVar(&o.reportDir, "report-dir", "report-"+time.Now().UTC().Format("20060102-150405.000000000"), "output directory")
	fs.IntVar(&o.queryLimit, "query-limit", 0, "maximum distinct queries per scenario (0 means all)")
	fs.IntVar(&o.repeat, "repeat", 1, "measured repetitions per query")
	fs.IntVar(&o.concurrency, "concurrency", 1, "single client concurrency level; overrides the default sweep when --concurrency-levels is omitted")
	levels = fs.String("concurrency-levels", "1,4,8", "comma-separated client concurrency levels; an explicit value overrides --concurrency; reuse the same data/index")
	mixed = fs.String("mixed-scenarios", "", "two ordinary SQL scenario IDs to also execute as concurrent paired queries")
	fs.IntVar(&o.stabilityRepeat, "stability-repeat", 30, "repetitions for stability scenarios (0 inherits --repeat)")
	fs.IntVar(&o.stabilityQueryLimit, "stability-query-limit", 5, "distinct queries for stability scenarios (0 inherits --query-limit)")
	fs.IntVar(&o.warmup, "warmup", 5, "unmeasured queries per scenario")
	fs.BoolVar(&o.keepDB, "keep-db", false, "retain the generated benchmark database")
	endpoints = fs.String("query-endpoints", "", "comma-separated host:port SQL endpoints for stability checks")
	return
}

func runConcurrencyLevels(fs *flag.FlagSet, raw string) ([]int, error) {
	var scalarExplicit, levelsExplicit bool
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "concurrency":
			scalarExplicit = true
		case "concurrency-levels":
			levelsExplicit = true
		}
	})
	if scalarExplicit && !levelsExplicit {
		raw = ""
	}
	return parseConcurrencyLevels(raw)
}
