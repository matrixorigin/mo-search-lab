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
	"fmt"
	"strconv"
	"strings"
)

type scenarioRun struct {
	Scenario loadedScenario
	Options  options
}

// All packs start with one worker. Additional load is explicitly selected.
func defaultPackProfile(scenarios []loadedScenario, o options) options {
	if o.defaultConcurrency {
		o.concurrency, o.concurrencyLevels = 1, []int{1}
	}
	return o
}

func parseConcurrencyLevels(raw string) ([]int, error) {
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 8 {
		return nil, fmt.Errorf("--concurrency-levels supports at most 8 levels")
	}
	levels := make([]int, 0, len(parts))
	seen := make(map[int]bool)
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 128 || seen[n] {
			return nil, fmt.Errorf("--concurrency-levels requires unique integers from 1 to 128")
		}
		seen[n] = true
		levels = append(levels, n)
	}
	return levels, nil
}

// Expand profiles before creating the test database. Execution remains serial
// between profiles and reuses the existing per-scenario workers and cleanup.
func planScenarioRuns(scenarios []loadedScenario, o options) ([]scenarioRun, error) {
	if err := validateMeasurementOptions(o); err != nil {
		return nil, err
	}
	if o.stabilityRepeat < 0 || o.stabilityRepeat > 100000 || o.stabilityQueryLimit < 0 {
		return nil, fmt.Errorf("stability-repeat must be 0..100000 and stability-query-limit nonnegative")
	}
	levels := o.concurrencyLevels
	if len(levels) == 0 {
		levels = []int{o.concurrency}
	}
	if len(levels) > 8 {
		return nil, fmt.Errorf("at most 8 concurrency levels are supported")
	}
	seen := make(map[int]bool)
	for _, level := range levels {
		if level < 1 || level > 128 || seen[level] {
			return nil, fmt.Errorf("concurrency levels must be unique integers from 1 to 128")
		}
		seen[level] = true
	}
	var runs []scenarioRun
	var totalExecutions, totalIDs int
	for _, s := range scenarios {
		profiles := levels
		if s.Oracle == "stable_multiset" || filteredScenario(s) {
			profiles = []int{1}
		}
		for _, level := range profiles {
			profile := o
			profile.concurrency = level
			if s.Oracle == "stable_multiset" {
				if o.stabilityRepeat > 0 {
					profile.repeat = o.stabilityRepeat
				}
				if o.stabilityQueryLimit > 0 {
					profile.queryLimit = o.stabilityQueryLimit
				}
				if profile.repeat < s.MinRepeats {
					return nil, fmt.Errorf("scenario %s requires at least %d repetitions; use --stability-repeat", s.ID, s.MinRepeats)
				}
			}
			count := len(s.QueriesData)
			if profile.queryLimit > 0 {
				count = min(count, profile.queryLimit)
			}
			budgetRepeats := profile.repeat
			if timedPerformance(s, profile) {
				budgetRepeats = 1
			}
			if count == 0 || s.TopK < 1 || profile.repeat < 1 || count > 100000/budgetRepeats || count > 1000000/budgetRepeats/s.TopK {
				return nil, fmt.Errorf("scenario %s: result retention limit exceeded or no queries; at most 100000 executions and 1000000 IDs per profile", s.ID)
			}
			if !timedPerformance(s, profile) {
				totalExecutions += count * profile.repeat
				totalIDs += count * profile.repeat * s.TopK
			}
			if len(o.concurrencyLevels) > 0 && (totalExecutions > 100000 || totalIDs > 1000000) {
				return nil, fmt.Errorf("concurrency sweep retains at most 100000 executions and 1000000 IDs in total; reduce query limits or repetitions")
			}
			runs = append(runs, scenarioRun{Scenario: s, Options: profile})
		}
	}
	if err := allocateMeasurementBudgets(runs, o); err != nil {
		return nil, err
	}
	return runs, nil
}

// Reserve fixed-count checks before admitting duration workloads. Across all
// isolated and paired profiles, JSON retains <=100k samples and <=1M IDs.
func allocateMeasurementBudgets(runs []scenarioRun, o options) error {
	fixedExecutions, fixedIDs, timed := 0, 0, 0
	count := func(run scenarioRun) {
		if timedPerformance(run.Scenario, run.Options) {
			timed++
			return
		}
		n := len(run.Scenario.QueriesData)
		if run.Options.queryLimit > 0 {
			n = min(n, run.Options.queryLimit)
		}
		fixedExecutions += n * run.Options.repeat
		fixedIDs += n * run.Options.repeat * run.Scenario.TopK
	}
	for _, run := range runs {
		count(run)
	}
	for _, pair := range mixedProfiles(runs, o) {
		count(pair[0])
		count(pair[1])
	}
	if timed == 0 {
		return nil
	}
	availableExecutions, availableIDs := 100000-fixedExecutions, 1000000-fixedIDs
	if availableExecutions < timed || availableIDs < timed {
		return fmt.Errorf("insufficient raw sample budget after reserving fixed-count checks")
	}
	for index := range runs {
		run := &runs[index]
		if !timedPerformance(run.Scenario, run.Options) {
			continue
		}
		run.Options.measurementLimit = availableExecutions / timed
		run.Options.qualityQueryLimit = availableIDs / timed / max(1, o.rounds) / run.Scenario.TopK
		if run.Options.qualityQueryLimit < 1 {
			return fmt.Errorf("insufficient quality sample budget for %s; reduce rounds or selected profiles", run.Scenario.ID)
		}
	}
	return nil
}
