// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Performance timing covers SQL execution and complete result consumption.
// Quality scores and sorting/aggregation are computed after workers finish.
type MeasurementRound struct {
	Number          int     `json:"number"`
	TargetSeconds   float64 `json:"target_seconds"`
	WarmupSeconds   float64 `json:"warmup_seconds"`
	WarmupErrors    int     `json:"warmup_errors"`
	MeasuredSeconds float64 `json:"measured_seconds"`
	QualitySeconds  float64 `json:"quality_seconds"`
	Executions      int     `json:"executions"`
	SQLSuccesses    int     `json:"sql_successes"`
	SQLFailures     int     `json:"sql_failures"`
	QPS             float64 `json:"qps"`
	P50MS           float64 `json:"p50_ms"`
	P90MS           float64 `json:"p90_ms"`
	P95MS           float64 `json:"p95_ms"`
	P99MS           float64 `json:"p99_ms"`
	StopReason      string  `json:"stop_reason"`
}

func validateMeasurementOptions(o options) error {
	if o.measureDuration < 0 || o.measureDuration > 24*time.Hour || o.warmupDuration < 0 || o.warmupDuration > time.Hour || o.cooldown < 0 || o.cooldown > time.Hour || o.rounds < 0 || o.rounds > 10 || o.measureDuration > 0 && o.rounds < 1 {
		return fmt.Errorf("measurement durations must be nonnegative (measure <=24h, warmup/cooldown <=1h), rounds must be 1..10")
	}
	return nil
}

func timedPerformance(s loadedScenario, o options) bool {
	if o.measureDuration <= 0 || s.Route != "sql" || s.Oracle != "ann_recall" && s.Oracle != "qrels" {
		return false
	}
	if filteredScenario(s) {
		return false
	}
	return true
}

func filteredScenario(s loadedScenario) bool {
	for _, q := range s.QueriesData {
		if len(q.AllowedIDRanges) > 0 {
			return true
		}
	}
	return false
}

func qualitySampleAvailable(s ScenarioReport, r QueryResult) bool {
	return s.PerformanceMode != "duration" || r.QualitySampled
}

type phaseRecord struct {
	index   int
	results []QueryResult
}
type measurementPhase struct {
	records     []phaseRecord
	seconds     float64
	errors      int
	routeErrors []int
	stopReason  string
}

// There is no queued work after the deadline. Each persistent worker claims
// its next request only while the window is open. In-flight SQL is drained,
// and its completion time is included in the actual denominator.
func runMeasurementPhase(ctx context.Context, concurrency int, duration time.Duration, limit, queryCount, qualityQueries int, collect bool, execute func(worker, index int) []QueryResult) measurementPhase {
	if duration <= 0 {
		return measurementPhase{stopReason: "deadline"}
	}
	var next atomic.Int64
	var capped atomic.Bool
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	records := make([][]phaseRecord, concurrency)
	errors := make([]int, concurrency)
	routeErrors := make([][]int, concurrency)
	var deadline time.Time
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-barrier
			for ctx.Err() == nil && time.Now().Before(deadline) {
				index := int(next.Add(1) - 1)
				if collect && index >= limit {
					capped.Store(true)
					break
				}
				results := execute(worker, index)
				if routeErrors[worker] == nil {
					routeErrors[worker] = make([]int, len(results))
				}
				for route := range results {
					r := &results[route]
					if !r.SQLSucceeded {
						errors[worker]++
						routeErrors[worker][route]++
					}
					if collect {
						r.QualitySampled = index < min(queryCount, qualityQueries)
						r.Pass = r.SQLSucceeded
						if !r.QualitySampled {
							r.IDs = nil
						}
					}
				}
				if collect {
					records[worker] = append(records[worker], phaseRecord{index: index, results: results})
				}
			}
		}(worker)
	}
	start := time.Now()
	deadline = start.Add(duration)
	close(barrier)
	wg.Wait()
	p := measurementPhase{seconds: time.Since(start).Seconds(), stopReason: "deadline"}
	for worker := range records {
		p.records = append(p.records, records[worker]...)
		p.errors += errors[worker]
		if len(routeErrors[worker]) > len(p.routeErrors) {
			p.routeErrors = make([]int, len(routeErrors[worker]))
		}
		for route, count := range routeErrors[worker] {
			p.routeErrors[route] += count
		}
	}
	if capped.Load() {
		p.stopReason = "sample_limit"
	}
	if ctx.Err() != nil {
		p.stopReason = "cancelled"
	}
	sort.Slice(p.records, func(i, j int) bool { return p.records[i].index < p.records[j].index })
	return p
}

func waitCooldown(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func runTimedMeasurement(ctx context.Context, reports []*ScenarioReport, scenarios []loadedScenario, queries [][]query, o options, execute func(worker, index int) []QueryResult) {
	rounds := max(1, o.rounds)
	limit := o.measurementLimit
	if limit == 0 {
		limit = 100000 / len(reports)
	}
	qualityQueries := o.qualityQueryLimit
	if qualityQueries == 0 {
		qualityQueries = min(len(queries[0]), 1000000/max(1, scenarios[0].TopK)/rounds/len(reports))
	}
	for _, report := range reports {
		report.PerformanceMode = "duration"
		report.Repetitions = 0
		if qualityQueries < len(queries[0]) {
			report.MeasurementWarnings = append(report.MeasurementWarnings, fmt.Sprintf("质量结果每轮最多保留前 %d/%d 条查询的首次请求", qualityQueries, len(queries[0])))
		}
	}
	for round := 1; round <= rounds && ctx.Err() == nil; round++ {
		logProgress(ctx, fmt.Sprintf("%s · C%d · 第 %d/%d 轮 · 预热 %s", reports[0].ID, o.concurrency, round, rounds, o.warmupDuration))
		warmup := runMeasurementPhase(ctx, o.concurrency, o.warmupDuration, 0, len(queries[0]), 0, false, execute)
		if ctx.Err() != nil {
			break
		}
		logProgress(ctx, fmt.Sprintf("%s · C%d · 第 %d/%d 轮 · 测量 %s", reports[0].ID, o.concurrency, round, rounds, o.measureDuration))
		phase := runMeasurementPhase(ctx, o.concurrency, o.measureDuration, limit-len(reports[0].Results), len(queries[0]), qualityQueries, true, execute)
		qualityStart := time.Now()
		offsets := make([]int, len(reports))
		for route, report := range reports {
			offsets[route] = len(report.Results)
		}
		for _, record := range phase.records {
			for route, result := range record.results {
				result.Round = round
				if result.QualitySampled {
					evaluateRawResult(&result, scenarios[route], queries[route][record.index%len(queries[route])])
				}
				reports[route].Results = append(reports[route].Results, result)
			}
		}
		qualitySeconds := time.Since(qualityStart).Seconds()
		for route, report := range reports {
			batch := ScenarioReport{PerformanceMode: "duration", Results: report.Results[offsets[route]:]}
			aggregateScenarioResults(&batch, phase.seconds)
			report.MeasurementRounds = append(report.MeasurementRounds, MeasurementRound{Number: round, TargetSeconds: o.measureDuration.Seconds(), WarmupSeconds: warmup.seconds, WarmupErrors: phaseRouteErrors(warmup, route), MeasuredSeconds: phase.seconds, QualitySeconds: qualitySeconds, Executions: batch.Executions, SQLSuccesses: batch.SQLSuccesses, SQLFailures: batch.SQLFailures, QPS: batch.QPS, P50MS: batch.P50MS, P90MS: batch.P90MS, P95MS: batch.P95MS, P99MS: batch.P99MS, StopReason: phase.stopReason})
			if phaseRouteErrors(warmup, route) > 0 {
				report.MeasurementWarnings = append(report.MeasurementWarnings, fmt.Sprintf("第 %d 轮预热发生 %d 次 SQL 错误", round, phaseRouteErrors(warmup, route)))
			}
			if phase.stopReason == "sample_limit" {
				report.MeasurementWarnings = append(report.MeasurementWarnings, "达到原始样本上限，提前结束；该轮未完成目标测量时间")
			}
		}
		logProgress(ctx, fmt.Sprintf("%s · 第 %d/%d 轮结束 · %.3fs · %d 请求 · %s", reports[0].ID, round, rounds, phase.seconds, len(phase.records), phase.stopReason))
		if phase.stopReason != "deadline" {
			break
		}
		if round < rounds && !waitCooldown(ctx, o.cooldown) {
			break
		}
	}
	for _, report := range reports {
		elapsed := 0.0
		for _, round := range report.MeasurementRounds {
			elapsed += round.MeasuredSeconds
		}
		aggregateScenarioResults(report, elapsed)
		summarizeMeasurementRounds(report)
		if report.QualityQueries < report.SelectedQueries {
			report.MeasurementWarnings = append(report.MeasurementWarnings, fmt.Sprintf("质量样本覆盖 %d/%d 条查询；未采样查询不计入质量均值", report.QualityQueries, report.SelectedQueries))
		}
		if ctx.Err() != nil {
			report.Error = ctx.Err().Error()
			report.Failures++
		}
	}
}

func summarizeMeasurementRounds(report *ScenarioReport) {
	n := len(report.MeasurementRounds)
	mean := 0.0
	for _, round := range report.MeasurementRounds {
		mean += round.QPS
	}
	if n > 0 {
		mean /= float64(n)
	}
	variance := 0.0
	for _, round := range report.MeasurementRounds {
		variance += (round.QPS - mean) * (round.QPS - mean)
	}
	if n > 1 {
		report.QPSStdDev = math.Sqrt(variance / float64(n-1))
	}
	if mean > 0 {
		report.QPSVariation = report.QPSStdDev / mean * 100
	}
	if report.SQLSuccesses < 1000 {
		report.MeasurementWarnings = append(report.MeasurementWarnings, "成功延迟样本少于 1000，P99 尾部样本有限")
	}
}

func phaseRouteErrors(phase measurementPhase, route int) int {
	if route < len(phase.routeErrors) {
		return phase.routeErrors[route]
	}
	return 0
}
