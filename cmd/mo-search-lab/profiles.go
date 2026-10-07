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
		if s.Oracle == "stable_multiset" {
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
			if count == 0 || s.TopK < 1 || profile.repeat < 1 || count > 100000/profile.repeat || count > 1000000/profile.repeat/s.TopK {
				return nil, fmt.Errorf("scenario %s: result retention limit exceeded or no queries; at most 100000 executions and 1000000 IDs per profile", s.ID)
			}
			totalExecutions += count * profile.repeat
			totalIDs += count * profile.repeat * s.TopK
			if len(o.concurrencyLevels) > 0 && (totalExecutions > 100000 || totalIDs > 1000000) {
				return nil, fmt.Errorf("concurrency sweep retains at most 100000 executions and 1000000 IDs in total; reduce query limits or repetitions")
			}
			runs = append(runs, scenarioRun{Scenario: s, Options: profile})
		}
	}
	return runs, nil
}
