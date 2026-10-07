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
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestStableMultisetIgnoresOrderButDetectsChangedMembership(t *testing.T) {
	report := ScenarioReport{Results: []QueryResult{
		{ID: "q", IDs: []string{"a", "a", "b", "c"}},
		{ID: "q", IDs: []string{"c", "b", "a", "a"}},
		{ID: "q", IDs: []string{"a", "b", "c", "d"}},
	}}
	evaluateStability(&report, []query{{ID: "q", ExactIDs: []string{"a", "a", "b", "c"}}}, 3, 4)
	if !report.Results[0].Pass || !report.Results[1].Pass || report.Results[2].Pass {
		t.Fatalf("unexpected pass flags: %+v", report.Results)
	}
	if len(report.Stability) != 1 || report.Stability[0].DistinctResults != 2 || report.Stability[0].MaxChangedIDs != 1 || report.Stability[0].WorstOverlap != 0.75 {
		t.Fatalf("unexpected stability metrics: %+v", report.Stability)
	}
}

func TestOrderedStabilityAndExplicitEmptyAllowance(t *testing.T) {
	ordered := ScenarioReport{CheckOrder: true, Results: []QueryResult{
		{ID: "q", SQLSucceeded: true, IDs: []string{"a", "b"}},
		{ID: "q", SQLSucceeded: true, IDs: []string{"b", "a"}},
	}}
	evaluateStability(&ordered, []query{{ID: "q"}}, 2, 0)
	if !ordered.Results[0].Pass || ordered.Results[1].Pass || ordered.Stability[0].DistinctResults != 1 || ordered.Stability[0].DistinctOrders != 2 || ordered.Stability[0].ReorderedExecutions != 1 {
		t.Fatalf("same collection, different ordering was not distinguished: %+v", ordered)
	}
	empty := ScenarioReport{CheckOrder: true, AllowEmpty: true, Results: []QueryResult{
		{ID: "q", SQLSucceeded: true, IDs: []string{}}, {ID: "q", SQLSucceeded: true, IDs: []string{}},
	}}
	evaluateStability(&empty, []query{{ID: "q"}}, 2, 0)
	if !empty.Results[0].Pass || !empty.Results[1].Pass || empty.Stability[0].DistinctResults != 1 {
		t.Fatalf("explicitly allowed empty repeat failed: %+v", empty)
	}
}

func TestStableButWrongResultFailsExactOracle(t *testing.T) {
	report := ScenarioReport{Results: []QueryResult{
		{ID: "q", IDs: []string{"a", "a"}},
		{ID: "q", IDs: []string{"a", "a"}},
	}}
	evaluateStability(&report, []query{{ID: "q", ExactIDs: []string{"a", "b"}}}, 2, 2)
	if report.Results[0].Pass || report.Results[1].Pass || report.Stability[0].DistinctResults != 1 {
		t.Fatalf("stable but wrong result passed: %+v", report)
	}
}

func TestSelectIDsCanExtractColumnAndPreserveDuplicates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("WITH docs AS").WillReturnRows(sqlmock.NewRows([]string{"id", "body"}).AddRow("1", "apple").AddRow("1", "apple"))
	ids, err := selectIDs(context.Background(), db, "WITH docs AS (SELECT 1) SELECT id, body FROM docs", nil, 2, time.Second, "id", true)
	if err != nil || len(ids) != 2 || ids[0] != "1" || ids[1] != "1" {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	mock.ExpectQuery("WITH docs AS").WillReturnRows(sqlmock.NewRows([]string{"id", "body"}).AddRow("1", "apple").AddRow("1", "apple"))
	_, err = selectIDs(context.Background(), db, "WITH docs AS (SELECT 1) SELECT id, body FROM docs", nil, 2, time.Second, "id", false)
	if err == nil || !strings.Contains(err.Error(), "duplicate ID") {
		t.Fatalf("expected duplicate rejection, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStabilityReportsSQLFailuresEvenWhenSuccessfulResultsAgree(t *testing.T) {
	report := ScenarioReport{Results: []QueryResult{
		{ID: "q", SQLSucceeded: true, IDs: []string{"a"}},
		{ID: "q", Error: "query timed out"},
		{ID: "q", SQLSucceeded: true, IDs: []string{"a"}},
	}}
	evaluateStability(&report, []query{{ID: "q"}}, 3, 1)
	aggregateScenarioResults(&report, 2)
	got := report.Stability[0]
	if got.DistinctResults != 1 || got.Executions != 3 || got.SQLSuccesses != 2 || got.Failures != 1 || report.SQLFailures != 1 || report.AssertionFailures != 0 || report.Failures != 1 {
		t.Fatalf("incomplete repeat check looked successful: %+v %+v", got, report)
	}
}

func TestStabilitySQLPreflight(t *testing.T) {
	if err := validateStatement("WITH t AS (SELECT id FROM documents) SELECT id FROM t", true); err != nil {
		t.Fatal(err)
	}
	if err := validateStatement("WITH t AS (SELECT 'DELETE' AS body FROM documents) SELECT body FROM t", true); err != nil {
		t.Fatalf("quoted keyword rejected: %v", err)
	}
	if err := validateStatement("WITH t AS (DELETE FROM documents) SELECT id FROM t", true); err == nil {
		t.Fatal("mutating CTE accepted")
	}
}

func TestPlanEvidenceAndRepetitionGate(t *testing.T) {
	if err := checkPlanEvidence("Remote MergeTop Remote", map[string]int{"Remote": 2, "MergeTop": 1}); err != nil {
		t.Fatal(err)
	}
	if err := checkPlanEvidence("Remote Limit", map[string]int{"Remote": 2}); err == nil {
		t.Fatal("one remote scope satisfied a two-scope requirement")
	}
	s := loadedScenario{scenario: scenario{Oracle: "stable_multiset", MinRepeats: 3}}
	report := runStabilityScenario(context.Background(), s, []query{{ID: "q"}}, options{repeat: 2}, nil, ScenarioReport{})
	if report.Failures != 1 || !strings.Contains(report.Error, "at least 3") {
		t.Fatalf("insufficient repetitions passed: %+v", report)
	}
}
