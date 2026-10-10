// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestDatasetConcurrencyChoicesDefaultOffAndStayLocal(t *testing.T) {
	root := t.TempDir()
	health := saveCataloguePack(t, root, "health", "gist1m_full", 1000000, 100, "vector.json", "vector_nprobe_20.json", "vector_nprobe_100.json", "vector_stability.json")
	filter := saveCataloguePack(t, root, "filter", "gist1m_filtered_v7", 1000000, 100, "filter.json")
	text := saveCataloguePack(t, root, "text", "t2ranking_anli_2303643_ngram_v3", 2303643, 100, "text.json")
	paired := saveCataloguePack(t, root, "paired", "gist_t2_sql_workload_v7", 3303643, 100, "vector.json", "fulltext.json")
	m := &terminalModel{width: 100, height: 32, packsRoot: root, reportsRoot: t.TempDir(), launchSeed: defaultTerminalOptions()}
	m.openLauncher()
	f := m.launcher
	requests, err := f.requests(m.launchSeed)
	if err != nil || len(requests) != 4 {
		t.Fatal(requests, err)
	}
	for _, request := range requests {
		if !reflect.DeepEqual(request.Options.concurrencyLevels, []int{1}) {
			t.Fatal("default enabled extra load", request.Options.concurrencyLevels)
		}
	}
	f.openPackChooser()
	f.movePack(1)
	if f.concurrencyChoice != 4 || f.packs[f.packChoice].Path != health {
		t.Fatal("nested concurrency focus is incorrect")
	}
	m.updateLauncher(tea.KeyMsg{Type: tea.KeySpace})
	f.movePack(1)
	m.updateLauncher(tea.KeyMsg{Type: tea.KeySpace})
	if !reflect.DeepEqual(f.levelsForPack(health), []int{1, 4, 8}) {
		t.Fatal("checkbox did not select additional load")
	}
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyEsc})
	if !reflect.DeepEqual(f.levelsForPack(health), []int{1}) {
		t.Fatal("Esc kept edited optional load")
	}
	f.toggleConcurrency(health, 4)
	f.toggleConcurrency(text, 8)
	f.toggleConcurrency(filter, 4)
	requests, err = f.requests(m.launchSeed)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]int{health: {1, 4}, filter: {1}, text: {1, 8}, paired: {1}}
	for _, request := range requests {
		if !reflect.DeepEqual(request.Options.concurrencyLevels, want[request.Options.pack]) {
			t.Fatal("selection leaked to another dataset", request.Label, request.Options.concurrencyLevels)
		}
	}
	f.openPackChooser()
	f.movePack(1)
	m.updateLauncher(tea.KeyMsg{Type: tea.KeySpace})
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyEsc})
	if !reflect.DeepEqual(f.levelsForPack(health), []int{1, 4}) {
		t.Fatal("Esc lost the previous load selection")
	}
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 40}} {
		m.width, m.height = size[0], size[1]
		f.openPackChooser()
		for _, choice := range f.packChoices() {
			f.packChoice, f.concurrencyChoice = choice.Pack, choice.Level
			body := ansi.Strip(m.View())
			if !strings.Contains(body, "› ") || !strings.Contains(body, "空格") || len(strings.Split(body, "\n")) > size[1] {
				t.Fatal("nested focus or actions hidden", size, choice, body)
			}
		}
	}
	f.openPackChooser()
	f.toggleVisiblePacks()
	f.toggleVisiblePacks()
	for _, path := range []string{health, filter, text, paired} {
		if !reflect.DeepEqual(f.levelsForPack(path), []int{1}) {
			t.Fatal("select all enabled optional load")
		}
	}
	f.togglePack(health)
	f.toggleConcurrency(health, 8)
	if f.packChecked(health) || f.concurrencyChecked(health, 8) {
		t.Fatal("child option silently enabled a dataset")
	}
}
