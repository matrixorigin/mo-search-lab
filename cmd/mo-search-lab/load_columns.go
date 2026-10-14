// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"fmt"
	"strings"
)

// A column list maps CSV fields to table columns. Empty entries consume a field
// through a LOAD DATA user variable, so packs can share one physical CSV.
func loadColumnList(columns []string) (string, error) {
	if len(columns) == 0 {
		return "", nil
	}
	if len(columns) > 256 {
		return "", fmt.Errorf("load columns exceed 256 fields")
	}
	fields := make([]string, len(columns))
	seen := make(map[string]bool)
	for index, column := range columns {
		if column == "" {
			fields[index] = fmt.Sprintf("@mo_search_lab_skip_%d", index+1)
			continue
		}
		key := strings.ToLower(column)
		if !safeIdentifier.MatchString(column) || seen[key] {
			return "", fmt.Errorf("invalid or duplicate load column %q", column)
		}
		seen[key] = true
		fields[index] = "`" + column + "`"
	}
	if len(seen) == 0 {
		return "", fmt.Errorf("load columns must map at least one table column")
	}
	return " (" + strings.Join(fields, ", ") + ")", nil
}
