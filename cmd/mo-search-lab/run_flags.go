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
	fs.StringVar(&o.host, "host", "127.0.0.1", "MO SQL 地址")
	fs.IntVar(&o.port, "port", 6001, "MO SQL 端口")
	fs.StringVar(&o.user, "user", "root", "MO SQL 账号")
	fs.StringVar(&o.password, "password", "", "MO SQL 密码；省略为空，UI 遮蔽显示，不写入报告")
	fs.DurationVar(&o.timeout, "timeout", 3*time.Minute, "单条 SQL/执行计划超时；性能测试的导入和建索引使用此预算的 10 倍")
}

func registerEnvironmentFlags(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.environmentFile, "environment-file", "", "本地部署/资源声明 JSON（可选）")
	fs.Var(&o.moConfigs, "mo-config", "本地 MO TOML 文件（可选，可重复）；补充文件显式配置")
	fs.StringVar(&o.monitoringConfig, "monitoring-config", "", "本地 Grafana/Prometheus 监控 JSON（可选）")
}

func registerRunFlags(fs *flag.FlagSet, o *options) (levels, mixed, endpoints *string) {
	fs.StringVar(&o.pack, "pack", "", "必填：版本化数据包目录")
	registerConnectionFlags(fs, o)
	registerEnvironmentFlags(fs, o)
	fs.StringVar(&o.reportDir, "report-dir", "report-"+time.Now().UTC().Format("20060102-150405.000000000"), "新报告目录；默认名称含 UTC 时间，不覆盖已有目录")
	fs.IntVar(&o.queryLimit, "query-limit", 0, "每个普通场景的查询数上限；0 为全部，不减少数据导入量")
	fs.IntVar(&o.repeat, "repeat", 1, "每条普通查询的测量次数；稳定性另用 --stability-repeat")
	fs.IntVar(&o.concurrency, "concurrency", 1, "普通查询的单个并发值；显式 --concurrency-levels 优先")
	levels = fs.String("concurrency-levels", "1", "普通查询并发档位，默认只跑 1；可指定 1,4,8，稳定性仍串行")
	mixed = fs.String("mixed-scenarios", "", "追加两路同时查询，如 vector,fulltext；使用包内两个普通 SQL 场景 ID")
	fs.IntVar(&o.stabilityRepeat, "stability-repeat", 30, "每条稳定性查询重复次数；0 继承 --repeat，需满足包内最小重复数")
	fs.IntVar(&o.stabilityQueryLimit, "stability-query-limit", 5, "稳定性查询数上限；0 继承 --query-limit")
	fs.IntVar(&o.warmup, "warmup", 5, "每个场景/并发档位的预热次数，不计入测量")
	fs.BoolVar(&o.keepDB, "keep-db", false, "保留生成的测试库；默认测量后清理")
	endpoints = fs.String("query-endpoints", "", "稳定性检查的 SQL 端点，以逗号分隔 host:port，最多 16 个")
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

func explicitRunConcurrency(fs *flag.FlagSet) bool {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "concurrency" || f.Name == "concurrency-levels" {
			explicit = true
		}
	})
	return explicit
}
