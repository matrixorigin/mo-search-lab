// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	_ "embed"
	"strings"
)

// This curated historical reference is presentation only. It is independent
// of the current run's evidence, concurrency selection and environment.
//
//go:embed report_es_reference.html
var esReferenceNotesHTML string

func hasESReferenceNotes(report Report) bool {
	id := strings.ToLower(report.Dataset)
	return report.RunKind != "environment_inspection" && (id == "t2ranking" || strings.HasPrefix(id, "t2ranking_") || strings.HasPrefix(id, "t2ranking-"))
}
