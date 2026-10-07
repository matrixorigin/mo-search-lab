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
	"math"
	"reflect"
	"testing"
)

func TestRRFAndQualityOracles(t *testing.T) {
	ids := fuseRRF([]string{"a", "b"}, []string{"b", "c"}, 2)
	if !reflect.DeepEqual(ids, []string{"b", "a"}) {
		t.Fatalf("RRF got %v", ids)
	}
	ann := scenario{Oracle: "ann_recall", TopK: 2, MinScore: 0.5}
	score, pass, message := scoreQuery(ann, query{ExactIDs: []string{"a", "b"}}, []string{"a", "c"})
	if !pass || score != 0.5 || message != "" {
		t.Fatalf("ANN: score=%v pass=%v message=%q", score, pass, message)
	}
	qrels := scenario{Oracle: "qrels", TopK: 2, MinScore: 0.9}
	score, pass, _ = scoreQuery(qrels, query{Relevance: map[string]int{"a": 2, "b": 1}}, []string{"b", "a"})
	if pass || math.IsNaN(score) || score <= 0 || score >= 0.9 {
		t.Fatalf("qrels expected a valid score below threshold, got score=%v pass=%v", score, pass)
	}
	exact := scenario{Oracle: "exact_ids", TopK: 2}
	_, pass, _ = scoreQuery(exact, query{ExactIDs: []string{"a", "b"}}, []string{"b", "a"})
	if pass {
		t.Fatal("order-sensitive exact oracle accepted swapped IDs")
	}
}

func TestPercentiles(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5}
	if percentile(values, 0.5) != 3 || percentile(values, 0.95) != 5 {
		t.Fatalf("unexpected nearest-rank percentiles")
	}
}

func TestObservedT2RankingMetricsAndLegacyGain(t *testing.T) {
	q := query{Relevance: map[string]int{"a": 3, "b": 1, "c": 2}}
	s := scenario{Oracle: "qrels", TopK: 2, QualityMode: "observe", NDCGGain: "linear", RelevantGrade: 2}
	ids := []string{"b", "a"}
	m := qualityMetrics(s, q, ids)
	want := (1 + 3/math.Log2(3)) / (3 + 2/math.Log2(3))
	if math.Abs(m.NDCG-want) > 1e-12 || m.NDCG10 != m.NDCG || m.Recall != 0.5 || m.MRR10 != 0.5 {
		t.Fatalf("hand-computed linear metric mismatch: %+v want %.12f", m, want)
	}
	if score, pass, _ := scoreQuery(s, q, nil); score != 0 || !pass {
		t.Fatal("observational empty result was treated as an execution/acceptance failure")
	}
	s.NDCGGain = ""
	want = (1 + 7/math.Log2(3)) / (7 + 3/math.Log2(3))
	if got := qualityMetrics(s, q, ids).NDCG; math.Abs(got-want) > 1e-12 {
		t.Fatalf("legacy exponential gain changed: %.12f want %.12f", got, want)
	}
	if _, pass, _ := scoreQuery(scenario{Oracle: "exact_ids", TopK: 10}, query{ExactIDs: []string{}}, nil); !pass {
		t.Fatal("hand-authored negative exact case did not pass")
	}
}
