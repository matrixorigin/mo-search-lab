// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import "path/filepath"

type terminalPackChoice struct{ Pack, Level int }

func terminalConcurrencyKey(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return filepath.Clean(path)
}

func cloneTerminalConcurrency(source map[string][]int) map[string][]int {
	copy := make(map[string][]int, len(source))
	for path, levels := range source {
		copy[path] = append([]int(nil), levels...)
	}
	return copy
}

func (f *terminalLauncher) packChoices() []terminalPackChoice {
	var choices []terminalPackChoice
	for _, index := range f.packIndices() {
		choices = append(choices, terminalPackChoice{Pack: index})
		if f.packs[index].concurrent {
			choices = append(choices, terminalPackChoice{index, 4}, terminalPackChoice{index, 8})
		}
	}
	return choices
}

func (f *terminalLauncher) concurrencyChecked(path string, level int) bool {
	if !f.packChecked(path) {
		return false
	}
	for _, selected := range f.packConcurrency[terminalConcurrencyKey(path)] {
		if selected == level {
			return true
		}
	}
	return false
}

func (f *terminalLauncher) toggleConcurrency(path string, level int) {
	if !f.packChecked(path) {
		f.err = "请先勾选该测试项目。"
		return
	}
	if level != 4 && level != 8 {
		return
	}
	supported := false
	for _, p := range f.packs {
		if sameTerminalPackPath(p.Path, path) {
			supported = p.concurrent
			break
		}
	}
	if !supported {
		f.err = "该项目只进行串行检查。"
		return
	}
	if f.packConcurrency == nil {
		f.packConcurrency = make(map[string][]int)
	}
	key := terminalConcurrencyKey(path)
	var levels []int
	for _, candidate := range []int{4, 8} {
		checked := f.concurrencyChecked(path, candidate)
		if candidate == level {
			checked = !checked
		}
		if checked {
			levels = append(levels, candidate)
		}
	}
	f.packConcurrency[key] = levels
	f.err = ""
}

func (f *terminalLauncher) levelsForPack(path string) []int {
	levels := []int{1}
	for _, pack := range f.packs {
		if sameTerminalPackPath(pack.Path, path) && pack.concurrent {
			for _, level := range []int{4, 8} {
				if f.concurrencyChecked(path, level) {
					levels = append(levels, level)
				}
			}
			break
		}
	}
	return levels
}
