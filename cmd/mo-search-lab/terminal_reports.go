// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const terminalReportLimit = 64 << 20

type terminalRun struct {
	Path, Dataset, Status, ToolVersion string
	StartedAt                          time.Time
}

type terminalDataset struct {
	ID   string
	Runs []terminalRun
}

type terminalDocument struct {
	View     reportView
	Returned []float64
	Evidence map[string]string
}

// A catalogue only needs the header; query results are loaded for one run.
func readTerminalHeader(path string) (terminalRun, error) {
	f, err := openTerminalFile(path, terminalReportLimit)
	if err != nil {
		return terminalRun{}, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 64<<10))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return terminalRun{}, fmt.Errorf("%s: expected report object", path)
	}
	run := terminalRun{Path: path}
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return run, fmt.Errorf("%s: read metadata: %w", path, err)
		}
		var target any
		switch key {
		case "dataset":
			target = &run.Dataset
		case "status":
			target = &run.Status
		case "started_at":
			target = &run.StartedAt
		case "tool_version":
			target = &run.ToolVersion
		default:
			var skip json.RawMessage
			target = &skip
		}
		if err := decoder.Decode(target); err != nil {
			return run, fmt.Errorf("%s: read metadata: %w", path, err)
		}
		seen[fmt.Sprint(key)] = true
		if seen["dataset"] && seen["status"] && seen["started_at"] && seen["tool_version"] {
			if run.Dataset == "" || run.ToolVersion == "" || run.StartedAt.IsZero() || (run.Status != "passed" && run.Status != "failed") {
				return run, fmt.Errorf("%s: invalid benchmark report metadata", path)
			}
			return run, nil
		}
	}
	return run, fmt.Errorf("%s: missing benchmark report metadata", path)
}

func openTerminalFile(path string, limit int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s: expected regular file of at most %d bytes", path, limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		_ = f.Close()
		return nil, fmt.Errorf("%s: file changed while opening", path)
	}
	return f, nil
}

func readTerminalJSON(path string, limit int64, target any) error {
	f, err := openTerminalFile(path, limit)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, limit+1))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s: trailing JSON or invalid input", path)
	}
	return nil
}

func discoverTerminalReports(root, single string) ([]terminalDataset, []string, error) {
	var runs []terminalRun
	var warnings []string
	if single != "" {
		run, err := readTerminalHeader(filepath.Join(single, "report.json"))
		if err != nil {
			return nil, nil, err
		}
		runs = append(runs, run)
	} else {
		visited := 0
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			visited++
			if visited > 4096 {
				return fmt.Errorf("report discovery exceeds 4096 entries; choose a smaller --reports directory")
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if entry.IsDir() && len(strings.Split(rel, string(filepath.Separator))) > 4 {
				return filepath.SkipDir
			}
			if entry.Name() != "report.json" || !entry.Type().IsRegular() {
				return nil
			}
			if len(runs)+len(warnings) >= 128 {
				return fmt.Errorf("report discovery exceeds 128 reports; choose a smaller --reports directory")
			}
			run, err := readTerminalHeader(path)
			if err != nil {
				warnings = append(warnings, err.Error())
			} else {
				runs = append(runs, run)
			}
			return nil
		})
		if err != nil {
			return nil, warnings, err
		}
	}
	if len(runs) == 0 {
		return nil, warnings, fmt.Errorf("no benchmark reports found; use --reports DIR or --report-dir DIR")
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].StartedAt.Equal(runs[j].StartedAt) {
			return runs[i].Path < runs[j].Path
		}
		return runs[i].StartedAt.After(runs[j].StartedAt)
	})
	var datasets []terminalDataset
	indices := make(map[string]int)
	for _, run := range runs {
		index, found := indices[run.Dataset]
		if !found {
			index = len(datasets)
			indices[run.Dataset] = index
			datasets = append(datasets, terminalDataset{ID: run.Dataset})
		}
		datasets[index].Runs = append(datasets[index].Runs, run)
	}
	return datasets, warnings, nil
}

func loadTerminalDocument(run terminalRun) (*terminalDocument, error) {
	var report Report
	if err := readTerminalJSON(run.Path, terminalReportLimit, &report); err != nil {
		return nil, err
	}
	if report.Dataset != run.Dataset || report.ToolVersion != run.ToolVersion || !report.StartedAt.Equal(run.StartedAt) || report.Status != run.Status {
		return nil, fmt.Errorf("report metadata changed; reopen ui to refresh the catalogue")
	}
	if len(report.Scenarios) == 0 || len(report.Scenarios) > 256 {
		return nil, fmt.Errorf("expected 1..256 measured scenarios")
	}
	var executions, ids int
	for _, scenario := range report.Scenarios {
		level := scenario.EffectiveConcurrency
		if level == 0 {
			level = report.Profile.Concurrency
		}
		if level < 1 || level > 128 || scenario.Repetitions < 0 || scenario.Repetitions > 100000 || scenario.MeasuredSeconds < 0 || math.IsInf(scenario.MeasuredSeconds, 0) {
			return nil, fmt.Errorf("scenario %s: invalid concurrency, repetition count or measurement duration", scenario.ID)
		}
		executions += len(scenario.Results)
		for _, result := range scenario.Results {
			ids += len(result.IDs)
			if result.Iteration < 0 || result.Iteration >= 100000 {
				return nil, fmt.Errorf("scenario %s: invalid repeat iteration", scenario.ID)
			}
			if result.Quality != nil {
				q := result.Quality
				for _, metric := range []float64{q.NDCG, q.NDCG10, q.Recall, q.MRR10} {
					if metric < 0 || metric > 1 || math.IsNaN(metric) {
						return nil, fmt.Errorf("scenario %s: invalid quality metric", scenario.ID)
					}
				}
			}
		}
	}
	if executions > 100000 || ids > 1000000 {
		return nil, fmt.Errorf("report exceeds 100000 executions or 1000000 result IDs")
	}
	view, err := makeReportView(report)
	if err != nil {
		return nil, err
	}
	doc := &terminalDocument{View: view, Evidence: make(map[string]string)}
	for _, scenario := range view.Scenarios {
		returned := 0
		for _, result := range scenario.Results {
			if result.SQLSucceeded {
				returned += len(result.IDs)
			}
		}
		mean := 0.0
		if scenario.SQLSuccesses > 0 {
			mean = float64(returned) / float64(scenario.SQLSuccesses)
		}
		doc.Returned = append(doc.Returned, mean)
	}
	for _, name := range []string{"environment.json", "stability-tie-analysis.json"} {
		path := filepath.Join(filepath.Dir(run.Path), name)
		var evidence map[string]json.RawMessage
		err := readTerminalJSON(path, 1<<20, &evidence)
		if os.IsNotExist(err) {
			doc.Evidence[name] = "未提供"
			continue
		}
		if err != nil {
			doc.Evidence[name] = "读取失败: " + err.Error()
			continue
		}
		// Per-execution evidence remains in the source file; the UI shows its header.
		delete(evidence, "records")
		body, err := json.MarshalIndent(evidence, "", "  ")
		if err != nil {
			return nil, err
		}
		doc.Evidence[name] = string(body)
	}
	return doc, nil
}
