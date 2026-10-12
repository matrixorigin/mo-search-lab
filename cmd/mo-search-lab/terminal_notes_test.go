// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestTerminalNotesShareHistoricalHTMLReference(t *testing.T) {
	root := t.TempDir()
	path := saveTerminalFixture(t, root, "text", terminalFixture("t2ranking_anli_2303643_ngram_v3"))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	datasets, _, err := discoverTerminalReports(root, "")
	if err != nil {
		t.Fatal(err)
	}
	m := &terminalModel{datasets: datasets, width: 100, height: 32, percentile: 95}
	m.load()
	if m.loadError != nil {
		t.Fatal(m.loadError)
	}
	m.level, m.scenario = 4, 1
	doc := m.doc
	terminalKey(m, "7")
	if m.section != terminalNotesSection || m.doc != doc || m.level != 4 || m.scenario != 1 {
		t.Fatal("notes key changed the selected report or filters")
	}
	for _, width := range []int{40, 60, 100, 140} {
		m.width = width
		var out bytes.Buffer
		if err := m.writePlain(&out); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"2026-10-06", "独立于本次 MO 运行", "213.26", "1165.50", "37.04", "0.3017", "31.08 GiB", "16 GiB", "20:12", "verification.json"} {
			// Wrapped text is normalized for matching prose and hardware values.
			text := strings.Join(strings.Fields(out.String()), " ")
			if !strings.Contains(strings.ReplaceAll(text, " ", ""), strings.ReplaceAll(want, " ", "")) {
				t.Fatalf("width %d lost %q: %s", width, want, out.String())
			}
		}
		for _, line := range m.contentLines() {
			if runewidth.StringWidth(line) > width || strings.ContainsAny(line, "\x1b\r") || strings.Contains(line, "<td>") {
				t.Fatalf("notes exceed terminal width or contain markup: %q", line)
			}
		}
	}
	terminalKey(m, "?")
	if m.section != terminalHelpSection {
		t.Fatal("metric help did not open from notes")
	}
	terminalKey(m, "?")
	if m.section != terminalNotesSection {
		t.Fatal("metric help did not return to notes")
	}
	terminalKey(m, "tab")
	if m.section != 0 {
		t.Fatal("notes did not wrap to overview")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("notes changed saved measurement evidence: %v", err)
	}
	m.doc.View.Dataset = "gist1m_full"
	if strings.Contains(strings.Join(m.notesLines(), "\n"), "213.26") {
		t.Fatal("ES measurement leaked into an unrelated report")
	}
}
