// Copyright 2026 Matrix Origin
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

var version = "dev"

type options struct {
	pack                string
	host                string
	port                int
	user                string
	password            string // Never serialized into reports.
	reportDir           string
	queryLimit          int
	repeat              int
	concurrency         int
	warmup              int
	timeout             time.Duration
	keepDB              bool
	queryEndpoints      []string
	concurrencyLevels   []int
	defaultConcurrency  bool
	stabilityRepeat     int
	stabilityQueryLimit int
	mixedScenarios      []string
	environmentFile     string
	moConfigs           configPaths
	monitoringConfig    string
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	args := os.Args[1:]
	if args[0] == "help" {
		if len(args) == 1 {
			writeRootHelp(os.Stdout)
			return
		}
		if len(args) != 2 || commandHelp[args[1]] == "" {
			fmt.Fprintln(os.Stderr, "用法：mo-search-lab help [ui|run|inspect|validate|render|version]")
			os.Exit(2)
		}
		args = []string{args[1], "--help"}
	}
	var err error
	switch args[0] {
	case "-h", "--help":
		writeRootHelp(os.Stdout)
		return
	case "validate":
		fs := flag.NewFlagSet("validate", flag.ExitOnError)
		pack := fs.String("pack", "", "必填：版本化数据包目录")
		configureCommandHelp(fs)
		_ = fs.Parse(args[1:])
		if *pack == "" {
			err = fmt.Errorf("--pack is required")
		} else {
			_, err = loadPack(*pack)
			if err == nil {
				fmt.Println("pack valid")
			}
		}
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		var o options
		levels, mixed, endpoints := registerRunFlags(fs, &o)
		configureCommandHelp(fs)
		_ = fs.Parse(args[1:])
		if o.pack == "" || o.repeat < 1 || o.repeat > 100000 || o.concurrency < 1 || o.concurrency > 128 || o.warmup < 0 || o.warmup > 10000 || o.queryLimit < 0 || o.timeout <= 0 || o.timeout > (1<<63-1)/10 || o.port < 1 || o.port > 65535 {
			err = fmt.Errorf("invalid run flags: --pack, repeat 1..100000, concurrency 1..128, warmup 0..10000, positive timeout/port, nonnegative query-limit are required")
		} else {
			o.concurrencyLevels, err = runConcurrencyLevels(fs, *levels)
			o.defaultConcurrency = !explicitRunConcurrency(fs)
			if *mixed != "" {
				for _, id := range strings.Split(*mixed, ",") {
					o.mixedScenarios = append(o.mixedScenarios, strings.TrimSpace(id))
				}
			}
			if *endpoints != "" {
				if strings.Count(*endpoints, ",") >= 16 {
					err = fmt.Errorf("--query-endpoints supports at most 16 endpoints")
				}
				seenEndpoints := make(map[string]bool)
				for _, endpoint := range strings.Split(*endpoints, ",") {
					if err != nil {
						break
					}
					endpoint = strings.TrimSpace(endpoint)
					host, portText, parseErr := net.SplitHostPort(endpoint)
					port, portErr := strconv.Atoi(portText)
					if parseErr != nil || host == "" || portErr != nil || port < 1 || port > 65535 || seenEndpoints[endpoint] {
						err = fmt.Errorf("invalid --query-endpoints entry %q; use host:port", endpoint)
						break
					}
					seenEndpoints[endpoint] = true
					o.queryEndpoints = append(o.queryEndpoints, endpoint)
				}
			}
			var p *pack
			if err == nil {
				p, err = loadPack(o.pack)
			}
			if err == nil {
				var runs []scenarioRun
				o = defaultPackProfile(p.Scenarios, o)
				runs, err = planScenarioRuns(p.Scenarios, o)
				if err == nil {
					err = validateMixedSelection(p.Scenarios, o, runs)
				}
			}
			if err == nil {
				err = ensureReportTarget(o.reportDir)
			}
			if err == nil {
				var report Report
				report, err = runBenchmark(context.Background(), p, o)
				if writeErr := writeReport(o.reportDir, report); writeErr != nil {
					if err == nil {
						err = fmt.Errorf("write report: %w", writeErr)
					} else {
						err = fmt.Errorf("%v; write report: %w", err, writeErr)
					}
				} else {
					fmt.Printf("report: %s/report.html\n", o.reportDir)
				}
			}
		}
	case "inspect":
		fs := flag.NewFlagSet("inspect", flag.ExitOnError)
		var o options
		registerConnectionFlags(fs, &o)
		registerEnvironmentFlags(fs, &o)
		fs.StringVar(&o.reportDir, "report-dir", "environment-"+time.Now().UTC().Format("20060102-150405.000000000"), "新环境报告目录；默认名称含 UTC 时间，不覆盖已有目录")
		configureCommandHelp(fs)
		_ = fs.Parse(args[1:])
		if o.port < 1 || o.port > 65535 || o.timeout <= 0 || o.timeout > (1<<63-1)/10 {
			err = fmt.Errorf("inspect requires a valid port and positive bounded timeout")
		} else if err = ensureReportTarget(o.reportDir); err == nil {
			var report Report
			report, err = inspectEnvironment(context.Background(), o)
			if report.Dataset != "" {
				if writeErr := writeReport(o.reportDir, report); writeErr != nil {
					err = fmt.Errorf("inspection: %v; write report: %w", err, writeErr)
				} else {
					fmt.Printf("report: %s/report.html\n", o.reportDir)
				}
			}
		}
	case "version":
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
			fmt.Fprint(os.Stdout, commandHelp["version"])
			return
		}
		fmt.Println(version)
	case "ui":
		err = runTerminalUI(args[1:])
	case "render":
		fs := flag.NewFlagSet("render", flag.ExitOnError)
		dir := fs.String("report-dir", "", "必填：含 report.json 的单份报告目录；仅更新 report.html")
		levels := fs.String("concurrency-levels", "", "只显示指定的已测并发档位；保留独立稳定性场景")
		scenarioIDs := fs.String("scenario-ids", "", "显示的已测普通场景 ID，以逗号分隔；保留独立稳定性场景")
		configureCommandHelp(fs)
		_ = fs.Parse(args[1:])
		if *dir == "" {
			err = fmt.Errorf("--report-dir is required")
		} else {
			var selected []int
			selected, err = parseConcurrencyLevels(*levels)
			if err == nil {
				selection := reportSelection{Levels: selected}
				if *scenarioIDs != "" {
					for _, id := range strings.Split(*scenarioIDs, ",") {
						selection.ScenarioIDs = append(selection.ScenarioIDs, strings.TrimSpace(id))
					}
				}
				err = renderSelectedSavedReport(*dir, selection)
			}
			if err == nil {
				fmt.Printf("report: %s/report.html\n", *dir)
			}
		}
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	writeRootHelp(os.Stderr)
}
