// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"fmt"
	"reflect"
	"regexp"
)

// Source identities describe frozen pack files; SQL in ScenarioReport is what
// was actually executed after the explicitly selected compatibility conversion.
type ScenarioSource struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	SQL    string `json:"sql"`
}

var legacyRankSuffix = regexp.MustCompile(`(?i)\s+by\s+rank\s+with\s+option\s+'mode\s*=\s*(pre|post)'\s*$`)
var legacyFilterID = regexp.MustCompile(`^vector_(pre|post)_([0-9]+)(_stability)?$`)

func compatibleScenarios(scenarios []loadedScenario, o options) ([]loadedScenario, error) {
	if !o.compat304 {
		return scenarios, nil
	}
	converted := make([]loadedScenario, 0, len(scenarios))
	seen := make(map[string]int)
	for _, source := range scenarios {
		s := source
		if legacyRankSuffix.MatchString(s.SQL) {
			s.CompatibilitySources = []ScenarioSource{{ID: source.ID, SHA256: source.Digest, SQL: source.SQL}}
			s.SQL = legacyRankSuffix.ReplaceAllString(s.SQL, "")
			// These built-in checks differ only by the unavailable pre/post
			// selector. Run their common legacy SQL once per query set.
			s.ID = legacyFilterID.ReplaceAllString(s.ID, "vector_default_${2}${3}")
		}
		if index, ok := seen[s.ID]; ok {
			previous := &converted[index]
			a, b := previous.scenario, s.scenario
			a.Queries.Path, b.Queries.Path = "", ""
			if len(previous.CompatibilitySources) == 0 || len(s.CompatibilitySources) == 0 ||
				previous.QueryDigest != s.QueryDigest || !reflect.DeepEqual(a, b) {
				return nil, fmt.Errorf("3.0.4 compatibility scenario collision: %s", s.ID)
			}
			previous.CompatibilitySources = append(previous.CompatibilitySources, s.CompatibilitySources...)
			continue
		}
		seen[s.ID] = len(converted)
		converted = append(converted, s)
	}
	return converted, nil
}
