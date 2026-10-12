// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/mattn/go-runewidth"
)

func (m *terminalModel) notesLines() []string {
	if !hasESReferenceNotes(m.doc.View.Report) {
		return []string{"备注", "", "本报告暂无补充备注。ES 历史实测在 T2Ranking 全文报告中展示。"}
	}
	lines, err := terminalReferenceNotes(esReferenceNotesHTML, m.textWidth())
	if err != nil {
		return []string{"备注读取失败", err.Error()}
	}
	return lines
}

// Read the same embedded reference used by HTML reports so the two displays
// cannot drift in their recorded values, conditions or hardware configuration.
// The input is our fixed fragment, not customer HTML or a network response.
func terminalReferenceNotes(fragment string, width int) ([]string, error) {
	d := xml.NewDecoder(strings.NewReader(fragment))
	d.Strict = false
	d.AutoClose = xml.HTMLAutoClose
	d.Entity = xml.HTMLEntity
	var lines []string
	var text strings.Builder
	var block string
	var row []string
	var table [][]string
	var cell, headingRow bool
	for {
		token, err := d.Token()
		if err == io.EOF {
			return lines, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode embedded reference: %w", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			switch token.Name.Local {
			case "summary", "h3", "p", "caption":
				block = token.Name.Local
				text.Reset()
			case "tr":
				row = nil
			case "td", "th":
				cell = true
				text.Reset()
			case "thead":
				headingRow = true
			case "br":
				text.WriteByte('\n')
			}
		case xml.CharData:
			if cell || block != "" {
				text.Write(token)
			}
		case xml.EndElement:
			switch token.Name.Local {
			case "summary", "h3", "p", "caption":
				lines = append(lines, strings.TrimSpace(text.String()), "")
				block = ""
			case "td", "th":
				row = append(row, strings.TrimSpace(text.String()))
				cell = false
			case "tr":
				table = append(table, row)
			case "table":
				lines = append(lines, terminalReferenceTable(table, headingRow, width)...)
				lines = append(lines, "")
				table, headingRow = nil, false
			}
		}
	}
}

func terminalReferenceTable(rows [][]string, headers bool, width int) []string {
	if len(rows) == 0 {
		return nil
	}
	var lines []string
	if !headers {
		for _, row := range rows {
			if len(row) == 2 {
				lines = append(lines, terminalKV(row[0], row[1], width)...)
			}
		}
		return lines
	}
	columns := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, value := range row {
			columns[i] = max(columns[i], runewidth.StringWidth(value))
		}
	}
	total := 2 * (len(columns) - 1)
	for _, column := range columns {
		total += column
	}
	if total <= width {
		for _, row := range rows {
			cells := make([]string, len(row))
			for i, value := range row {
				cells[i] = terminalPad(value, columns[i])
			}
			lines = append(lines, strings.TrimRight(strings.Join(cells, "  "), " "))
		}
		return lines
	}
	// Narrow windows use labeled values instead of breaking a table mid-column.
	for _, row := range rows[1:] {
		lines = append(lines, row[0])
		for i, value := range row[1:] {
			lines = append(lines, terminalKV(rows[0][i+1], value, width)...)
		}
		lines = append(lines, "")
	}
	return lines
}
