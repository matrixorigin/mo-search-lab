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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmokePackContract(t *testing.T) {
	p, err := loadPack("testdata/smoke")
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest.Dataset != "smoke_8" || len(p.Scenarios) != 5 || len(p.LoadPaths) != 1 {
		t.Fatalf("unexpected pack: %#v", p)
	}
	for _, s := range p.Scenarios {
		if len(s.QueriesData) == 0 {
			t.Fatalf("scenario %s has no queries", s.ID)
		}
	}
}

func TestVersionedEvaluationOptionsAndEmptyTruth(t *testing.T) {
	valid := scenario{Oracle: "qrels", QualityMode: "observe", NDCGGain: "linear", RelevantGrade: 2}
	if err := validateEvaluationOptions(valid, 3); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []scenario{
		{Oracle: "qrels", QualityMode: "observe", MinScore: 0.01},
		{Oracle: "exact_ids", QualityMode: "observe"},
		{Oracle: "qrels", NDCGGain: "unknown"},
		{Oracle: "qrels", RelevantGrade: -1},
		{Oracle: "qrels", CheckOrder: true},
		{Oracle: "stable_multiset", AllowEmpty: true, ExpectedRows: 1},
	} {
		if err := validateEvaluationOptions(invalid, 3); err == nil {
			t.Fatalf("accepted invalid evaluation options: %+v", invalid)
		}
	}
	if err := validateEvaluationOptions(valid, 2); err == nil {
		t.Fatal("schema 2 accepted schema 3 options")
	}
	for _, tc := range []struct {
		oracle, input string
		valid         bool
	}{
		{"exact_ids", `{"id":"negative","params":{},"exact_ids":[]}`, true},
		{"exact_ids", `{"id":"missing","params":{}}`, false},
		{"ann_recall", `{"id":"empty","params":{},"exact_ids":[]}`, false},
		{"qrels", `{"id":"grade1","params":{},"relevance":{"a":1}}`, false},
		{"qrels", `{"id":"grade2","params":{},"relevance":{"a":2}}`, true},
	} {
		_, err := parseQueries([]byte(tc.input), scenario{Oracle: tc.oracle, RelevantGrade: 2})
		if (err == nil) != tc.valid {
			t.Fatalf("%s: valid=%v err=%v", tc.input, tc.valid, err)
		}
	}
}

func TestPackRejectsTamperedQueryFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/smoke")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "vector.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":"changed"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPack(dir); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected hash mismatch, got %v", err)
	}
}

func TestPackRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.csv")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.csv")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	p := &pack{Root: root}
	if _, err := p.safePath("escape.csv"); err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("expected path escape rejection, got %v", err)
	}
}

func TestSQLShapeRejectsMutationInScenario(t *testing.T) {
	for _, statement := range []string{"DELETE FROM documents", "SELECT id FROM documents; DROP TABLE documents", ""} {
		if err := validateStatement(statement, true); err == nil {
			t.Fatalf("accepted %q", statement)
		}
	}
	if err := validateStatement("SELECT id FROM documents", true); err != nil {
		t.Fatal(err)
	}
}

func TestSQLScenarioSessionSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/smoke")); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	scenarioPath := filepath.Join(dir, "vector.json")
	var m manifest
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		t.Fatal(err)
	}
	var s scenario
	scenarioBytes, err := os.ReadFile(scenarioPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(scenarioBytes, &s); err != nil {
		t.Fatal(err)
	}
	writeScenario := func(statement string) {
		t.Helper()
		s.SessionSQL = []string{statement}
		body, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(scenarioPath, body, 0o600); err != nil {
			t.Fatal(err)
		}
		m.Scenarios[0].SHA256 = digest(body)
		body, err = json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeScenario("SET probe_limit=20")
	p, err := loadPack(dir)
	if err != nil || len(p.Scenarios[0].SessionSQL) != 1 {
		t.Fatalf("session setting was not accepted: pack=%v err=%v", p, err)
	}
	writeScenario("SET GLOBAL probe_limit=20")
	if _, err := loadPack(dir); err == nil || !strings.Contains(err.Error(), "session-scoped SET") {
		t.Fatalf("global setting was accepted: %v", err)
	}
}
