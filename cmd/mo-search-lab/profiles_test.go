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
	"flag"
	"io"
	"reflect"
	"testing"
	"time"
)

func TestConcurrencyLevelsAndIndependentStabilityProfiles(t *testing.T) {
	levels, err := parseConcurrencyLevels("1, 4,8,16")
	if err != nil || !reflect.DeepEqual(levels, []int{1, 4, 8, 16}) {
		t.Fatalf("levels=%v err=%v", levels, err)
	}
	for _, invalid := range []string{"0", "129", "1,1", "1,", "2,,4", "x", "1,2,3,4,5,6,7,8,9"} {
		if _, err := parseConcurrencyLevels(invalid); err == nil {
			t.Fatalf("accepted levels %q", invalid)
		}
	}
	if levels, err := parseConcurrencyLevels(""); err != nil || levels != nil {
		t.Fatalf("empty flag should preserve legacy execution: %v %v", levels, err)
	}
	queries := []query{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	ordinary := loadedScenario{scenario: scenario{ID: "recall", Oracle: "ann_recall", TopK: 10}, QueriesData: queries}
	stable := loadedScenario{scenario: scenario{ID: "stable", Oracle: "stable_multiset", TopK: 10, MinRepeats: 3}, QueriesData: queries}
	o := options{concurrency: 3, concurrencyLevels: []int{1, 4}, repeat: 1, queryLimit: 2, stabilityRepeat: 30, stabilityQueryLimit: 1}
	second := ordinary
	second.ID = "recall2"
	runs, err := planScenarioRuns([]loadedScenario{ordinary, stable, second}, o)
	if err != nil || len(runs) != 5 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	for i, expected := range []struct{ concurrency, repeat, limit int }{{1, 1, 2}, {4, 1, 2}, {1, 30, 1}, {1, 1, 2}, {4, 1, 2}} {
		got := runs[i].Options
		if got.concurrency != expected.concurrency || got.repeat != expected.repeat || got.queryLimit != expected.limit {
			t.Fatalf("profile %d: %+v", i, got)
		}
	}
	if o.repeat != 1 || o.concurrency != 3 || len(stable.QueriesData) != 3 {
		t.Fatal("planning mutated the requested options or pack")
	}
	legacy, err := planScenarioRuns([]loadedScenario{ordinary, stable}, options{concurrency: 3, repeat: 3, queryLimit: 2})
	if err != nil || len(legacy) != 2 || legacy[0].Options.concurrency != 3 || legacy[1].Options.concurrency != 1 || legacy[1].Options.repeat != 3 || legacy[1].Options.queryLimit != 2 {
		t.Fatalf("legacy profile inheritance changed: %v %v", legacy, err)
	}
	for _, invalid := range []options{
		{concurrency: 1, repeat: 1},
		{concurrency: 1, repeat: 3, stabilityRepeat: -1},
		{concurrency: 1, repeat: 3, stabilityQueryLimit: -1},
		{concurrency: 1, repeat: 3, concurrencyLevels: []int{4, 4}},
		{concurrency: 1, repeat: 100000},
	} {
		if _, err := planScenarioRuns([]loadedScenario{ordinary, stable}, invalid); err == nil {
			t.Fatalf("invalid profile accepted: %+v", invalid)
		}
	}
	ordinary.QueriesData = queries[:1]
	if _, err := planScenarioRuns([]loadedScenario{ordinary}, options{concurrencyLevels: []int{1, 4}, repeat: 100000}); err == nil {
		t.Fatal("combined sweep retention budget was not enforced")
	}
}

func TestRunHealthDefaultsAndOverrides(t *testing.T) {
	parse := func(args ...string) options {
		t.Helper()
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		var o options
		levels, _, _ := registerRunFlags(fs, &o)
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		var err error
		o.concurrencyLevels, err = runConcurrencyLevels(fs, *levels)
		o.defaultConcurrency = !explicitRunConcurrency(fs)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	defaults := parse()
	if defaults.queryLimit != 0 || defaults.repeat != 1 || defaults.warmup != 5 || defaults.stabilityRepeat != 30 || defaults.stabilityQueryLimit != 5 || defaults.timeout != 3*time.Minute {
		t.Fatalf("default health profile incomplete: %+v", defaults)
	}
	queries := make([]query, 6)
	ordinary := loadedScenario{scenario: scenario{ID: "quality", Oracle: "ann_recall", TopK: 100}, QueriesData: queries}
	stable := loadedScenario{scenario: scenario{ID: "stability", Oracle: "stable_multiset", TopK: 100, MinRepeats: 3}, QueriesData: queries}
	runs, err := planScenarioRuns([]loadedScenario{ordinary, stable}, defaults)
	if err != nil || len(runs) != 2 {
		t.Fatalf("default planning: %+v %v", runs, err)
	}
	for i, expected := range []int{1} {
		if runs[i].Options.concurrency != expected || runs[i].Options.queryLimit != 0 || runs[i].Options.repeat != 1 {
			t.Fatalf("missing full-query concurrency profile %d: %+v", expected, runs[i].Options)
		}
	}
	if runs[1].Options.concurrency != 1 || runs[1].Options.repeat != 30 || runs[1].Options.queryLimit != 5 {
		t.Fatalf("default stability profile: %+v", runs[1].Options)
	}
	for _, test := range []struct {
		name   string
		args   []string
		levels []int
		scalar int
	}{
		{"defaults", nil, []int{1}, 1},
		{"single explicit", []string{"--concurrency", "4"}, nil, 4},
		{"single explicit one", []string{"--concurrency", "1"}, nil, 1},
		{"custom sweep", []string{"--concurrency-levels", "1,8"}, []int{1, 8}, 1},
		{"levels win before scalar", []string{"--concurrency-levels", "1,8", "--concurrency", "4"}, []int{1, 8}, 4},
		{"levels win after scalar", []string{"--concurrency", "4", "--concurrency-levels", "1,8"}, []int{1, 8}, 4},
		{"empty sweep", []string{"--concurrency-levels="}, nil, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			o := parse(test.args...)
			if !reflect.DeepEqual(o.concurrencyLevels, test.levels) || o.concurrency != test.scalar {
				t.Fatalf("override precedence: %+v", o)
			}
		})
	}
	overrides := parse("--repeat", "3", "--query-limit", "2", "--stability-repeat", "0", "--stability-query-limit", "0", "--warmup", "0", "--timeout", "10s")
	runs, err = planScenarioRuns([]loadedScenario{ordinary, stable}, overrides)
	if err != nil || runs[1].Options.repeat != 3 || runs[1].Options.queryLimit != 2 || runs[1].Options.warmup != 0 || runs[1].Options.timeout != 10*time.Second {
		t.Fatalf("explicit zero/inheritance/timeout overridden by defaults: %+v %v", runs, err)
	}
	ordinary.QueriesData = make([]query, 10001)
	if _, err := planScenarioRuns([]loadedScenario{ordinary}, defaults); err == nil {
		t.Fatal("default complete sweep bypassed aggregate result-ID admission")
	}
}

func TestEveryPackDefaultsToSerialAndExplicitProfilesRemainAvailable(t *testing.T) {
	queries := []query{{ID: "q", AllowedIDRanges: [][2]int64{{0, 999}}}}
	ordinary := loadedScenario{scenario: scenario{ID: "pre", Route: "sql", Oracle: "ann_recall", TopK: 100}, QueriesData: queries}
	stable := loadedScenario{scenario: scenario{ID: "repeat", Route: "sql", Oracle: "stable_multiset", TopK: 100, MinRepeats: 30}, QueriesData: queries}
	pack := []loadedScenario{ordinary, stable}
	requested := options{defaultConcurrency: true, concurrency: 1, concurrencyLevels: []int{1, 4, 8}, repeat: 1, stabilityRepeat: 30}
	serial := defaultPackProfile(pack, requested)
	runs, err := planScenarioRuns(pack, serial)
	if err != nil || len(runs) != 2 || runs[0].Options.concurrency != 1 || runs[1].Options.concurrency != 1 || runs[1].Options.repeat != 30 || !reflect.DeepEqual(serial.concurrencyLevels, []int{1}) {
		t.Fatalf("filter-only pack still sweeps concurrency: %+v %v", runs, err)
	}
	if !reflect.DeepEqual(requested.concurrencyLevels, []int{1, 4, 8}) {
		t.Fatal("serial planning mutated original options")
	}
	explicit := requested
	explicit.defaultConcurrency = false
	if got := defaultPackProfile(pack, explicit); !reflect.DeepEqual(got.concurrencyLevels, []int{1, 4, 8}) {
		t.Fatal("explicit investigation profile was ignored")
	}
	mixed := requested
	mixed.mixedScenarios = []string{"pre", "text"}
	if got := defaultPackProfile(pack, mixed); !reflect.DeepEqual(got.concurrencyLevels, []int{1}) {
		t.Fatal("paired SQL default is not serial")
	}
	unfiltered := ordinary
	unfiltered.QueriesData = []query{{ID: "q"}}
	text := ordinary
	text.Oracle = "qrels"
	for _, scenarios := range [][]loadedScenario{{unfiltered}, {ordinary, text}, {stable}, {}} {
		if got := defaultPackProfile(scenarios, requested); !reflect.DeepEqual(got.concurrencyLevels, []int{1}) {
			t.Fatal("pack default is not serial")
		}
	}
}
