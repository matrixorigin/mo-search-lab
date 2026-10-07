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
	"math"
	"sort"
	"strconv"
)

func fuseRRF(vectorIDs, fulltextIDs []string, topK int) []string {
	scores := make(map[string]float64)
	for _, route := range [][]string{vectorIDs, fulltextIDs} {
		seen := make(map[string]bool)
		for i, id := range route {
			if seen[id] {
				continue
			}
			seen[id] = true
			scores[id] += 1 / float64(60+i+1)
		}
	}
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] != scores[ids[j]] {
			return scores[ids[i]] > scores[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > topK {
		ids = ids[:topK]
	}
	return ids
}

func scoreQuery(s scenario, q query, ids []string) (float64, bool, string) {
	if err := checkAllowedIDs(q, ids); err != nil {
		return 0, false, err.Error()
	}
	var score float64
	switch s.Oracle {
	case "nonempty":
		if len(ids) == 0 {
			return 0, false, "empty result"
		}
		score = 1
	case "exact_ids":
		want := q.ExactIDs
		if len(want) > s.TopK {
			want = want[:s.TopK]
		}
		if len(ids) != len(want) {
			return 0, false, fmt.Sprintf("IDs %v differ from %v", ids, want)
		}
		for i := range ids {
			if ids[i] != want[i] {
				return 0, false, fmt.Sprintf("IDs %v differ from %v", ids, want)
			}
		}
		score = 1
	case "ann_recall":
		want := q.ExactIDs
		if len(want) > s.TopK {
			want = want[:s.TopK]
		}
		set := make(map[string]bool, len(want))
		for _, id := range want {
			set[id] = true
		}
		seen := make(map[string]bool)
		for _, id := range ids {
			if set[id] && !seen[id] {
				score++
				seen[id] = true
			}
		}
		score /= float64(len(set))
	case "qrels":
		positive := false
		for _, grade := range q.Relevance {
			if grade < 0 {
				return 0, false, "negative relevance grade"
			}
			positive = positive || grade > 0
		}
		if !positive {
			return 0, false, "no positive relevance judgment"
		}
		metrics := qualityMetrics(s, q, ids)
		score = metrics.NDCG
	default:
		return 0, false, "unknown oracle"
	}
	if score < s.MinScore {
		return score, false, fmt.Sprintf("score %.4f below minimum %.4f", score, s.MinScore)
	}
	return score, true, ""
}

func checkAllowedIDs(q query, ids []string) error {
	if len(q.AllowedIDRanges) == 0 {
		return nil
	}
	for _, text := range ids {
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return fmt.Errorf("non-numeric result ID %q with numeric eligibility constraint", text)
		}
		index := sort.Search(len(q.AllowedIDRanges), func(i int) bool { return q.AllowedIDRanges[i][1] >= id })
		if index == len(q.AllowedIDRanges) || id < q.AllowedIDRanges[index][0] {
			return fmt.Errorf("result ID %q violates eligibility constraint", text)
		}
	}
	return nil
}

// Gain omission deliberately preserves the original pack scoring convention.
type QualityMetrics struct {
	NDCG   float64 `json:"ndcg"`
	NDCG10 float64 `json:"ndcg_at_10"`
	Recall float64 `json:"recall"`
	MRR10  float64 `json:"mrr_at_10"`
}

func qualityMetrics(s scenario, q query, ids []string) QualityMetrics {
	grades := make([]int, 0, len(q.Relevance))
	positive := make(map[string]bool)
	for id, grade := range q.Relevance {
		grades = append(grades, grade)
		if grade >= max(1, s.RelevantGrade) {
			positive[id] = true
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(grades)))
	ndcg := func(k int) float64 {
		var observed, ideal float64
		for i := 0; i < min(k, len(grades)); i++ {
			ideal += selectedGain(grades[i], i, s.NDCGGain)
		}
		for i, id := range ids[:min(k, len(ids))] {
			observed += selectedGain(q.Relevance[id], i, s.NDCGGain)
		}
		if ideal == 0 {
			return 0
		}
		return observed / ideal
	}
	metrics := QualityMetrics{NDCG: ndcg(s.TopK), NDCG10: ndcg(min(10, s.TopK))}
	seen := make(map[string]bool)
	for i, id := range ids[:min(s.TopK, len(ids))] {
		if positive[id] && !seen[id] {
			metrics.Recall++
			seen[id] = true
			if i < 10 && metrics.MRR10 == 0 {
				metrics.MRR10 = 1 / float64(i+1)
			}
		}
	}
	if len(positive) > 0 {
		metrics.Recall /= float64(len(positive))
	}
	return metrics
}

func selectedGain(grade, rank int, mode string) float64 {
	if mode == "linear" {
		return float64(grade) / math.Log2(float64(rank)+2)
	}
	return gain(grade, rank)
}

func gain(grade, rank int) float64 {
	return (math.Pow(2, float64(grade)) - 1) / math.Log2(float64(rank)+2)
}
