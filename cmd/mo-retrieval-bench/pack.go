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
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type fileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type loadSpec struct {
	File  fileRef `json:"file"`
	Table string  `json:"table"`
	Rows  int64   `json:"rows"`
}

type manifest struct {
	SchemaVersion int        `json:"schema_version"`
	Dataset       string     `json:"dataset"`
	DDL           []string   `json:"ddl"`
	Loads         []loadSpec `json:"loads"`
	Indexes       []string   `json:"indexes"`
	Scenarios     []fileRef  `json:"scenarios"`
}

type scenario struct {
	ID            string         `json:"id"`
	Route         string         `json:"route"`
	SQL           string         `json:"sql,omitempty"`
	VectorSQL     string         `json:"vector_sql,omitempty"`
	FulltextSQL   string         `json:"fulltext_sql,omitempty"`
	Args          []string       `json:"args,omitempty"`
	VectorArgs    []string       `json:"vector_args,omitempty"`
	FulltextArgs  []string       `json:"fulltext_args,omitempty"`
	Queries       fileRef        `json:"queries"`
	Oracle        string         `json:"oracle"`
	TopK          int            `json:"top_k"`
	CandidateK    int            `json:"candidate_k,omitempty"`
	MinScore      float64        `json:"min_score,omitempty"`
	IDColumn      string         `json:"id_column,omitempty"`
	ExpectedRows  int            `json:"expected_rows,omitempty"`
	MinRepeats    int            `json:"min_repetitions,omitempty"`
	SessionSQL    []string       `json:"session_sql,omitempty"`
	PlanContains  map[string]int `json:"plan_must_contain,omitempty"`
	QualityMode   string         `json:"quality_mode,omitempty"`
	NDCGGain      string         `json:"ndcg_gain,omitempty"`
	RelevantGrade int            `json:"relevant_grade,omitempty"`
	CheckOrder    bool           `json:"check_order,omitempty"`
	AllowEmpty    bool           `json:"allow_empty,omitempty"`
}

type query struct {
	ID              string         `json:"id"`
	Params          map[string]any `json:"params"`
	ExactIDs        []string       `json:"exact_ids,omitempty"`
	Relevance       map[string]int `json:"relevance,omitempty"`
	AllowedIDRanges [][2]int64     `json:"allowed_id_ranges,omitempty"`
}

type loadedScenario struct {
	scenario
	QueriesData []query
	Digest      string
	QueryDigest string
}

type pack struct {
	Root      string
	Manifest  manifest
	Digest    string
	Scenarios []loadedScenario
	LoadPaths []string
}

const maxJSONFileSize = 256 << 20

var safeIdentifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

func loadPack(dir string) (*pack, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	p := &pack{Root: root}
	manifestPath, err := p.safePath("manifest.json")
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := decodeStrict(raw, &m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.SchemaVersion < 1 || m.SchemaVersion > 3 || !safeIdentifier.MatchString(m.Dataset) || len(m.DDL) == 0 || len(m.Loads) == 0 || len(m.Scenarios) == 0 {
		return nil, errors.New("manifest requires schema_version=1, 2 or 3, a safe dataset name, DDL, loads, and scenarios")
	}
	for _, statement := range append(append([]string{}, m.DDL...), m.Indexes...) {
		if err := validateStatement(statement, false); err != nil {
			return nil, fmt.Errorf("setup SQL: %w", err)
		}
	}
	p.Manifest, p.Digest = m, digest(raw)
	for _, load := range m.Loads {
		if !safeIdentifier.MatchString(load.Table) || load.Rows < 1 || !strings.HasSuffix(strings.ToLower(load.File.Path), ".csv") {
			return nil, fmt.Errorf("invalid load table or row count: %q", load.Table)
		}
		path, _, err := p.verifyFile(load.File)
		if err != nil {
			return nil, err
		}
		p.LoadPaths = append(p.LoadPaths, path)
	}
	seen := make(map[string]bool)
	for _, ref := range m.Scenarios {
		_, body, err := p.verifyFile(ref)
		if err != nil {
			return nil, err
		}
		var s scenario
		if err := decodeStrict(body, &s); err != nil {
			return nil, fmt.Errorf("scenario %s: %w", ref.Path, err)
		}
		if !safeIdentifier.MatchString(s.ID) || seen[s.ID] || s.TopK < 1 || s.TopK > 1000 || s.MinScore < 0 || s.MinScore > 1 {
			return nil, fmt.Errorf("invalid or duplicate scenario %q", s.ID)
		}
		if s.CandidateK == 0 {
			s.CandidateK = s.TopK
		}
		if s.CandidateK < s.TopK || s.CandidateK > 1000 {
			return nil, fmt.Errorf("scenario %s: candidate_k must be in [top_k, 1000]", s.ID)
		}
		if s.Route == "sql" && s.CandidateK != s.TopK {
			return nil, fmt.Errorf("scenario %s: SQL route requires candidate_k=top_k", s.ID)
		}
		seen[s.ID] = true
		if s.Route != "sql" && s.Route != "hybrid_rrf" {
			return nil, fmt.Errorf("scenario %s: unsupported route %q", s.ID, s.Route)
		}
		if s.Route == "sql" && s.SQL == "" || s.Route == "hybrid_rrf" && (s.VectorSQL == "" || s.FulltextSQL == "") {
			return nil, fmt.Errorf("scenario %s: missing SQL", s.ID)
		}
		if err := validateEvaluationOptions(s, m.SchemaVersion); err != nil {
			return nil, fmt.Errorf("scenario %s: %w", s.ID, err)
		}
		if (s.Oracle == "ann_recall" || s.Oracle == "qrels") && s.QualityMode != "observe" && s.MinScore == 0 {
			return nil, fmt.Errorf("scenario %s: quality oracle requires min_score > 0", s.ID)
		}
		if s.Oracle == "stable_multiset" {
			if m.SchemaVersion < 2 || s.Route != "sql" || !safeIdentifier.MatchString(s.IDColumn) || s.MinScore != 0 || s.ExpectedRows < 0 || s.ExpectedRows > s.TopK {
				return nil, fmt.Errorf("scenario %s: stable_multiset requires schema version 2, SQL route, id_column, and valid expected_rows", s.ID)
			}
			if s.MinRepeats == 0 {
				s.MinRepeats = 2
			}
			if s.MinRepeats < 2 || s.MinRepeats > 100000 {
				return nil, fmt.Errorf("scenario %s: min_repetitions must be in [2, 100000]", s.ID)
			}
			if len(s.PlanContains) > 10 {
				return nil, fmt.Errorf("scenario %s: too many plan assertions", s.ID)
			}
			for term, count := range s.PlanContains {
				if term == "" || len(term) > 100 || count < 1 || count > 10 {
					return nil, fmt.Errorf("scenario %s: invalid plan assertion", s.ID)
				}
			}
		} else if s.ExpectedRows != 0 || s.MinRepeats != 0 || len(s.PlanContains) > 0 {
			return nil, fmt.Errorf("scenario %s: stability fields require stable_multiset oracle", s.ID)
		}
		if s.IDColumn != "" && (!safeIdentifier.MatchString(s.IDColumn) || s.Route != "sql" || (s.Oracle != "stable_multiset" && m.SchemaVersion < 3)) {
			return nil, fmt.Errorf("scenario %s: ordinary id_column requires schema 3 and SQL route", s.ID)
		}
		if s.Oracle != "stable_multiset" && len(s.SessionSQL) > 0 && (m.SchemaVersion < 2 || s.Route != "sql") {
			return nil, fmt.Errorf("scenario %s: session_sql requires schema version 2 and SQL route", s.ID)
		}
		if len(s.SessionSQL) > 10 {
			return nil, fmt.Errorf("scenario %s: too many session statements", s.ID)
		}
		for _, statement := range s.SessionSQL {
			upper := strings.ToUpper(strings.TrimSpace(statement))
			if strings.Contains(statement, ";") || !strings.HasPrefix(upper, "SET ") || strings.HasPrefix(upper, "SET GLOBAL ") || strings.HasPrefix(upper, "SET PERSIST ") || strings.Contains(upper, "@@GLOBAL") {
				return nil, fmt.Errorf("scenario %s: session SQL must be one session-scoped SET statement", s.ID)
			}
		}
		for _, statement := range []string{s.SQL, s.VectorSQL, s.FulltextSQL} {
			if statement == "" {
				continue
			}
			if err := validateStatement(statement, true); err != nil {
				return nil, fmt.Errorf("scenario %s: %w", s.ID, err)
			}
		}
		switch s.Oracle {
		case "nonempty", "exact_ids", "ann_recall", "qrels", "stable_multiset":
		default:
			return nil, fmt.Errorf("scenario %s: unsupported oracle %q", s.ID, s.Oracle)
		}
		_, queryBytes, err := p.verifyFile(s.Queries)
		if err != nil {
			return nil, err
		}
		queries, err := parseQueries(queryBytes, s)
		if err != nil {
			return nil, fmt.Errorf("scenario %s: %w", s.ID, err)
		}
		for _, q := range queries {
			if len(q.AllowedIDRanges) > 0 && m.SchemaVersion < 3 {
				return nil, fmt.Errorf("query %s: allowed_id_ranges requires schema version 3", q.ID)
			}
		}
		p.Scenarios = append(p.Scenarios, loadedScenario{scenario: s, QueriesData: queries, Digest: digest(body), QueryDigest: digest(queryBytes)})
	}
	return p, nil
}

func decodeStrict(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing JSON content")
	}
	return nil
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (p *pack) verifyFile(ref fileRef) (string, []byte, error) {
	if ref.Path == "" || len(ref.SHA256) != 64 {
		return "", nil, fmt.Errorf("path and SHA-256 required for %q", ref.Path)
	}
	path, err := p.safePath(ref.Path)
	if err != nil {
		return "", nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	if !strings.HasSuffix(strings.ToLower(path), ".csv") {
		info, err := f.Stat()
		if err != nil {
			return "", nil, err
		}
		if info.Size() > maxJSONFileSize {
			return "", nil, fmt.Errorf("JSON pack file exceeds %d bytes: %s", maxJSONFileSize, ref.Path)
		}
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), ref.SHA256) {
		return "", nil, fmt.Errorf("SHA-256 mismatch: %s", ref.Path)
	}
	if strings.HasSuffix(strings.ToLower(path), ".csv") {
		return path, nil, nil
	}
	body, err := os.ReadFile(path)
	return path, body, err
}

func validateStatement(statement string, queryOnly bool) error {
	trimmed := strings.TrimSpace(statement)
	upper := strings.ToUpper(trimmed)
	if trimmed == "" || strings.Contains(trimmed, ";") {
		return errors.New("SQL must be one statement without semicolons")
	}
	if queryOnly {
		if !strings.HasPrefix(upper, "SELECT ") && !strings.HasPrefix(upper, "WITH ") {
			return errors.New("query SQL must start with SELECT or WITH")
		}
		if containsMutatingSQLKeyword(trimmed) {
			return errors.New("query SQL contains a mutating keyword")
		}
	} else if !strings.HasPrefix(upper, "CREATE TABLE ") && !strings.HasPrefix(upper, "CREATE INDEX ") && !strings.HasPrefix(upper, "CREATE FULLTEXT INDEX ") {
		return errors.New("setup SQL must create a table or index")
	}
	return nil
}

func containsMutatingSQLKeyword(statement string) bool {
	mutating := map[string]bool{"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true, "DROP": true,
		"ALTER": true, "TRUNCATE": true, "CREATE": true, "MERGE": true, "CALL": true, "DO": true}
	var word strings.Builder
	var quote byte
	flush := func() bool {
		if word.Len() == 0 {
			return false
		}
		value := strings.ToUpper(word.String())
		word.Reset()
		return mutating[value]
	}
	for i := 0; i < len(statement); i++ {
		c := statement[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(statement) {
				i++
				continue
			}
			if c == quote {
				if i+1 < len(statement) && statement[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' {
			if flush() {
				return true
			}
			quote = c
			continue
		}
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' {
			word.WriteByte(c)
		} else if flush() {
			return true
		}
	}
	return flush()
}

func (p *pack) safePath(name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("absolute pack path is forbidden: %s", name)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(p.Root, name))
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(p.Root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("pack path escapes root: %s", name)
	}
	return path, nil
}

func parseQueries(raw []byte, s scenario) ([]query, error) {
	var result []query
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if len(result) >= 100000 {
			return nil, errors.New("query file exceeds 100000 rows")
		}
		// JSON arrays decoded directly into [2]int64 can be silently padded or
		// truncated. Validate their length before accepting eligibility bounds.
		type queryFields query
		var record struct {
			queryFields
			AllowedIDRanges [][]int64 `json:"allowed_id_ranges,omitempty"`
		}
		if err := decodeStrict(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		q := query(record.queryFields)
		for _, bounds := range record.AllowedIDRanges {
			if len(bounds) != 2 {
				return nil, fmt.Errorf("query %s: each allowed ID range must contain exactly two integers", q.ID)
			}
			q.AllowedIDRanges = append(q.AllowedIDRanges, [2]int64{bounds[0], bounds[1]})
		}
		if q.ID == "" || seen[q.ID] {
			return nil, fmt.Errorf("missing or duplicate query ID %q", q.ID)
		}
		seen[q.ID] = true
		for _, arg := range append(append(append([]string{}, s.Args...), s.VectorArgs...), s.FulltextArgs...) {
			if _, ok := q.Params[arg]; !ok {
				return nil, fmt.Errorf("query %s lacks parameter %s", q.ID, arg)
			}
		}
		if s.Oracle == "ann_recall" && len(q.ExactIDs) == 0 || s.Oracle == "exact_ids" && q.ExactIDs == nil {
			return nil, fmt.Errorf("query %s lacks exact_ids", q.ID)
		}
		if len(q.ExactIDs) > 0 {
			if s.Oracle == "stable_multiset" && (len(q.ExactIDs) > s.TopK || (s.ExpectedRows > 0 && len(q.ExactIDs) != s.ExpectedRows)) {
				return nil, fmt.Errorf("query %s has exact_ids length inconsistent with top_k/expected_rows", q.ID)
			}
			idSeen := make(map[string]bool, len(q.ExactIDs))
			for _, id := range q.ExactIDs {
				if id == "" || (idSeen[id] && s.Oracle != "stable_multiset") {
					return nil, fmt.Errorf("query %s has an empty or duplicate exact ID", q.ID)
				}
				idSeen[id] = true
			}
		}
		if len(q.AllowedIDRanges) > 256 {
			return nil, fmt.Errorf("query %s exceeds 256 allowed ID ranges", q.ID)
		}
		for i, bounds := range q.AllowedIDRanges {
			if bounds[0] > bounds[1] || (i > 0 && bounds[0] <= q.AllowedIDRanges[i-1][1]) {
				return nil, fmt.Errorf("query %s: allowed ID ranges must be ordered, nonoverlapping and nonempty", q.ID)
			}
		}
		if err := checkAllowedIDs(q, q.ExactIDs); err != nil {
			return nil, fmt.Errorf("query %s: invalid filtered truth: %w", q.ID, err)
		}
		if s.Oracle == "qrels" && len(q.Relevance) == 0 {
			return nil, fmt.Errorf("query %s lacks relevance judgments", q.ID)
		}
		if s.Oracle == "qrels" {
			positive := false
			for id, grade := range q.Relevance {
				if id == "" || grade < 0 || grade > 30 {
					return nil, fmt.Errorf("query %s has invalid relevance judgment", q.ID)
				}
				positive = positive || grade >= max(1, s.RelevantGrade)
			}
			if !positive {
				return nil, fmt.Errorf("query %s has no positive relevance judgment", q.ID)
			}
		}
		result = append(result, q)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("empty query file")
	}
	return result, nil
}

func validateEvaluationOptions(s scenario, schema int) error {
	if s.QualityMode == "" && s.NDCGGain == "" && s.RelevantGrade == 0 && !s.CheckOrder && !s.AllowEmpty {
		return nil
	}
	if schema < 3 {
		return errors.New("evaluation options require schema version 3")
	}
	if (s.CheckOrder || s.AllowEmpty) && s.Oracle != "stable_multiset" || s.AllowEmpty && s.ExpectedRows != 0 {
		return errors.New("check_order/allow_empty require stable_multiset; allow_empty requires expected_rows=0")
	}
	if s.QualityMode != "" && s.QualityMode != "observe" || s.NDCGGain != "" && s.NDCGGain != "linear" && s.NDCGGain != "exponential" || s.RelevantGrade < 0 || s.RelevantGrade > 30 {
		return errors.New("invalid quality_mode, ndcg_gain or relevant_grade")
	}
	if s.QualityMode != "" || s.NDCGGain != "" || s.RelevantGrade != 0 {
		if (s.Oracle != "qrels" && s.Oracle != "ann_recall") || s.QualityMode == "observe" && s.MinScore != 0 || s.Oracle == "ann_recall" && (s.NDCGGain != "" || s.RelevantGrade != 0) {
			return errors.New("quality options require qrels or ann_recall; ANN excludes gain/grade options; observe requires min_score=0")
		}
	}
	return nil
}
