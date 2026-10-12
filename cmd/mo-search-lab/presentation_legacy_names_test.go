// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNeutralDisplaysPreserveHistoricalEvidenceAndLinks(t *testing.T) {
	markup := `<p>anli_sql_bm25</p><a href="/t2ranking-anli/report.json" title="ANLI 结果">结果</a><script>const id="anli_sql_bm25";if(1<2){}</script><style>.anli{color:red}</style>`
	page := neutralApplicationHTML(markup)
	for _, want := range []string{`<p>app_sql_bm25</p>`, `title="app 结果"`, `href="/t2ranking-anli/report.json"`, `const id="anli_sql_bm25"`, `.anli{color:red}`} {
		if !strings.Contains(page, want) {
			t.Fatalf("neutral display lost %q", want)
		}
	}
	r := terminalFixture("t2ranking_anli_2303643_ngram_v3")
	r.DatasetName = "T2Ranking · anli 历史检索"
	r.Scenarios[0].ID = "anli_sql_bm25"
	before, _ := json.Marshal(r)
	var rendered bytes.Buffer
	if err := renderReportHTML(&rendered, r); err != nil {
		t.Fatal(err)
	}
	if legacyApplicationName.MatchString(rendered.String()) {
		t.Fatal("customer name remains in benchmark display")
	}
	view, err := makeReportView(r)
	if err != nil {
		t.Fatal(err)
	}
	m := &terminalModel{doc: &terminalDocument{View: view, Returned: make([]float64, len(view.Scenarios))}, width: 100, height: 32}
	for section := range terminalSections {
		m.section = section
		if legacyApplicationName.MatchString(strings.Join(m.contentLines(), "\n")) {
			t.Fatalf("customer name remains on terminal page %d", section)
		}
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) || m.doc.View.Dataset != r.Dataset || m.doc.View.Scenarios[0].ID != r.Scenarios[0].ID {
		t.Fatal("neutral display changed historical identifiers or evidence")
	}
}
