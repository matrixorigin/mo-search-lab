// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

func saveCataloguePack(t *testing.T, root, dir, dataset string, rows int64, topK int, scenes ...string) string {
	t.Helper()
	path := filepath.Join(root, dir)
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}
	m := manifest{Dataset: dataset, Loads: []loadSpec{{Rows: rows, File: fileRef{Path: "missing.csv"}}}}
	for _, scene := range scenes {
		m.Scenarios = append(m.Scenarios, fileRef{Path: scene})
	}
	if len(scenes) > 0 {
		body, _ := json.Marshal(map[string]int{"top_k": topK})
		if err := os.WriteFile(filepath.Join(path, scenes[0]), body, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	body, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(path, "manifest.json"), body, 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPackCatalogueRoutineScopeAndSelection(t *testing.T) {
	root := t.TempDir()
	healthScenes := []string{"vector.json", "vector_nprobe_20.json", "vector_nprobe_100.json", "vector_stability.json"}
	health := saveCataloguePack(t, root, "renamed-health", "gist1m_full", 1000000, 100, healthScenes...)
	legacy := saveCataloguePack(t, root, "legacy-health", "gist1m_full", 1000000, 10, healthScenes...)
	saveCataloguePack(t, root, "filter", "gist1m_filtered_v7", 1000000, 100)
	saveCataloguePack(t, root, "text", "t2ranking_anli_2303643_ngram_v3", 2303643, 100)
	paired := saveCataloguePack(t, root, "paired", "gist_t2_sql_workload_v7", 3303643, 100)
	saveCataloguePack(t, root, "smoke", "smoke_8", 8, 10)
	m := &terminalModel{width: 80, height: 24, packsRoot: root, reportsRoot: t.TempDir(), launchSeed: defaultTerminalOptions()}
	m.openLauncher()
	f := m.launcher
	if f.fields[launchPack].Value != health || len(f.selectedPacks) != 4 || f.selectionLabel() != "全部 4 项" {
		t.Fatal("default did not select all routine tests")
	}
	f.openPackChooser()
	if len(f.packIndices()) != 4 {
		t.Fatalf("routine menu includes legacy/smoke packs: %v", f.packIndices())
	}
	for _, title := range []string{"向量性能与召回", "向量过滤检查", "全文检索评测", "两路同时查询"} {
		if !strings.Contains(m.View(), title) {
			t.Fatalf("missing clear purpose: %s", title)
		}
	}
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 40}} {
		m.width, m.height, m.color = size[0], size[1], true
		for _, index := range f.packIndices() {
			f.packChoice = index
			view := ansi.Strip(m.View())
			if !strings.Contains(view, "› [x] "+f.packs[index].Name) || !strings.Contains(view, "Enter 完成") || len(strings.Split(view, "\n")) > size[1] {
				t.Fatalf("selection or guidance hidden at %v: %s", size, view)
			}
			for _, line := range strings.Split(view, "\n") {
				if runewidth.StringWidth(line) > size[0] {
					t.Fatal("catalogue overflow", line)
				}
			}
		}
	}
	m.width, m.height = 100, 32
	f.setSelectedPacks([]string{legacy})
	f.openPackChooser()
	if !f.allPacks || f.packs[f.packChoice].Path != legacy {
		t.Fatal("explicit legacy selection was replaced")
	}
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if f.allPacks || !f.packs[f.packChoice].Featured {
		t.Fatal("returning to routine list retained a hidden selection")
	}
	for _, index := range f.packIndices() {
		if f.packs[index].Path == paired {
			f.packChoice = index
		}
	}
	f.setSelectedPacks([]string{paired})
	m.updateLauncher(tea.KeyMsg{Type: tea.KeyEnter})
	request, err := f.request(m.launchSeed)
	if err != nil || request.Options.pack != paired || strings.Join(request.Options.mixedScenarios, ",") != "vector,fulltext" || !request.Options.defaultConcurrency {
		t.Fatal("friendly menu changed selected pack or execution profile", err)
	}
	m.launchSeed.pack = paired
	m.launchPacks = nil
	m.openLauncher()
	if m.launcher.fields[launchMixed].Value != "vector,fulltext" {
		t.Fatal("direct paired pack did not set paired SQL scenes")
	}
}

func TestCustomPackPresentationIsBoundedAndOptional(t *testing.T) {
	root := t.TempDir()
	path := saveCataloguePack(t, root, "customer-regression", "customer_case", 100, 100)
	metadata := `{"name":"客户召回稳定性复现","summary":"重复相同输入，检查返回 ID 与顺序。","scale":"100 条固定输入","featured":true}`
	if err := os.WriteFile(filepath.Join(path, "pack-info.json"), []byte(metadata), 0o640); err != nil {
		t.Fatal(err)
	}
	packs, err := discoverTerminalPacks(root)
	if err != nil || len(packs) != 1 || packs[0].Name != "客户召回稳定性复现" || !packs[0].Featured || packs[0].Dataset != "customer_case" || packs[0].Rows != 100 {
		t.Fatalf("optional metadata altered identity or failed to describe extension: %+v %v", packs, err)
	}
	if err := os.WriteFile(filepath.Join(path, "pack-info.json"), []byte(metadata+strings.Repeat(" ", 16<<10)), 0o640); err != nil {
		t.Fatal(err)
	}
	packs, err = discoverTerminalPacks(root)
	if err != nil || packs[0].Name != "customer_case" || packs[0].Featured {
		t.Fatal("oversized optional metadata was accepted or invalidated usable manifest")
	}
	outside := filepath.Join(root, "outside.json")
	if err := os.WriteFile(outside, []byte(metadata), 0o640); err != nil {
		t.Fatal(err)
	}
	var info terminalPackInfo
	if previewPackJSON(path, "../outside.json", 16<<10, &info) {
		t.Fatal("preview read metadata outside pack")
	}
}
