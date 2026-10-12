// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0
package main

import (
	"regexp"
	"strings"
)

var legacyApplicationName = regexp.MustCompile(`(?i)anli`)
var displayHTMLAttribute = regexp.MustCompile(`\b(?:aria-label|title|alt)="[^"]*"`)

// Historical packs keep their frozen identifiers. Displays use a neutral
// application alias; the underlying files, lookup keys and URLs stay valid.
func neutralApplicationText(text string) string {
	return legacyApplicationName.ReplaceAllString(text, "app")
}

func neutralApplicationHTML(body string) string {
	var out strings.Builder
	for len(body) > 0 {
		start := strings.IndexByte(body, '<')
		if start < 0 {
			out.WriteString(neutralApplicationText(body))
			break
		}
		out.WriteString(neutralApplicationText(body[:start]))
		body = body[start:]
		// Preserve scripts, CSS and link targets. They can reference the frozen
		// legacy identifiers and must continue to address the original data.
		lower := strings.ToLower(body[:min(len(body), 8)])
		closing := ""
		if strings.HasPrefix(lower, "<script") {
			closing = "</script>"
		} else if strings.HasPrefix(lower, "<style") {
			closing = "</style>"
		}
		if closing != "" {
			end := strings.Index(strings.ToLower(body), closing)
			if end < 0 {
				out.WriteString(body)
				break
			}
			end += len(closing)
			out.WriteString(body[:end])
			body = body[end:]
			continue
		}
		end := strings.IndexByte(body, '>')
		if end < 0 {
			out.WriteString(neutralApplicationText(body))
			break
		}
		out.WriteString(displayHTMLAttribute.ReplaceAllStringFunc(body[:end+1], neutralApplicationText))
		body = body[end+1:]
	}
	return out.String()
}
