// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadColumnsRejectInvalidMappings(t *testing.T) {
	for _, columns := range [][]string{
		{"", ""}, {"id", "ID"}, {"id", "x`); DROP TABLE documents; --"},
		{"id", "@user_variable"}, make([]string, 257),
	} {
		if _, err := loadColumnList(columns); err == nil {
			t.Fatalf("accepted invalid columns: %q", columns)
		}
	}
	got, err := loadColumnList([]string{"id", "", "embedding"})
	if err != nil || got != " (`id`, @mo_search_lab_skip_2, `embedding`)" {
		t.Fatalf("mapping: %q, %v", got, err)
	}
	if got, err := loadColumnList(nil); err != nil || got != "" {
		t.Fatalf("legacy load changed: %q, %v", got, err)
	}
}

func TestLoadColumnsRequireVersionFourBeforeSQL(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("testdata/smoke")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	m.Loads[0].Columns = []string{"id", "body", "embedding", "allow_access", "delete_flag"}
	for _, schema := range []int{1, 2, 3, 4} {
		m.SchemaVersion = schema
		body, err = json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := loadPack(dir)
		if schema < 4 {
			if err == nil || !strings.Contains(err.Error(), "load columns require schema version 4") {
				t.Fatalf("schema %d accepted new import mapping: %v", schema, err)
			}
		} else if err != nil || len(p.Manifest.Loads[0].Columns) != 5 {
			t.Fatalf("schema 4 mapping lost: %v", err)
		}
	}
}
