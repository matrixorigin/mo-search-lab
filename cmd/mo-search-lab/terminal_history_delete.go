// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func checkTerminalReportEntries(root *os.Root, d *terminalReportDeletion, names []string) error {
	if len(names) != len(d.Entries) {
		return fmt.Errorf("报告目录内容已变化；请重新选择后删除")
	}
	for _, name := range names {
		info, err := root.Lstat(name)
		expected := d.Entries[name]
		if err != nil || expected == nil || !os.SameFile(info, expected) || info.Mode() != expected.Mode() || info.Size() != expected.Size() || !info.ModTime().Equal(expected.ModTime()) {
			return fmt.Errorf("报告文件 %q 已变化；请重新选择后删除", name)
		}
	}
	return nil
}

func prepareTerminalHistoryDeletion(history terminalHistory, selected terminalRun, members []terminalRun) (*terminalReportDeletion, error) {
	if !history.Batch {
		d, err := prepareTerminalReportDeletion(selected)
		if err != nil {
			return nil, err
		}
		d.History, d.Members = &history, []*terminalReportDeletion{d}
		return d, nil
	}
	dir, err := filepath.Abs(history.Dir)
	if err != nil || filepath.Dir(dir) == dir {
		return nil, fmt.Errorf("运行目录无效")
	}
	d := &terminalReportDeletion{Run: selected, History: &history, Dir: dir}
	d.DirInfo, err = os.Lstat(dir)
	if err != nil || !d.DirInfo.IsDir() {
		return nil, fmt.Errorf("运行目录已变化或不是普通目录")
	}
	batch, err := readTerminalBatchRecord(dir)
	if err != nil || !batch.StartedAt.Equal(history.StartedAt) || !batch.FinishedAt.Equal(history.FinishedAt) {
		return nil, fmt.Errorf("运行汇总已变化，请重新打开历史列表")
	}
	d.BatchInfo, err = os.Lstat(filepath.Join(dir, "batch.json"))
	if err != nil || !d.BatchInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("运行汇总不是普通文件")
	}
	for _, run := range members {
		member, err := prepareTerminalReportDeletion(run)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", run.DisplayName(), err)
		}
		if filepath.Dir(member.Dir) != dir {
			return nil, fmt.Errorf("报告不在本次运行目录内")
		}
		d.Members = append(d.Members, member)
		d.Files += member.Files
	}
	if len(d.Members) == 0 {
		return nil, fmt.Errorf("本次运行没有可删除的报告")
	}
	d.ReportInfo = d.Members[0].ReportInfo
	d.Files++ // batch.json is removed only after all reports are removed.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := checkTerminalHistoryRoot(root, d); err != nil {
		return nil, err
	}
	return d, nil
}

func checkTerminalHistoryRoot(root *os.Root, d *terminalReportDeletion) error {
	dir, err := root.Stat(".")
	if err != nil || !os.SameFile(dir, d.DirInfo) {
		return fmt.Errorf("运行目录已变化")
	}
	batch, err := root.Lstat("batch.json")
	if err != nil || !unchangedTerminalReport(batch, d.BatchInfo) {
		return fmt.Errorf("运行汇总已变化；本次未删除")
	}
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(130)
	if err != nil || len(entries) != len(d.Members)+1 {
		return fmt.Errorf("运行目录包含未确认的文件或报告；本次未删除")
	}
	allowed := map[string]os.FileInfo{"batch.json": d.BatchInfo}
	for _, member := range d.Members {
		allowed[filepath.Base(member.Dir)] = member.DirInfo
	}
	for _, entry := range entries {
		info, err := root.Lstat(entry.Name())
		if expected := allowed[entry.Name()]; err != nil || expected == nil || !os.SameFile(info, expected) || info.Mode() != expected.Mode() {
			return fmt.Errorf("运行目录包含未确认或变化的条目 %q；本次未删除", entry.Name())
		}
	}
	return nil
}

func checkTerminalHistoryMember(root *os.Root, member *terminalReportDeletion) error {
	name := filepath.Base(member.Dir)
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() || !os.SameFile(info, member.DirInfo) {
		return fmt.Errorf("报告目录已变化")
	}
	child, err := root.OpenRoot(name)
	if err != nil {
		return err
	}
	defer child.Close()
	info, err = child.Stat(".")
	if err != nil || !os.SameFile(info, member.DirInfo) {
		return fmt.Errorf("报告目录已变化")
	}
	report, err := child.Lstat("report.json")
	if err != nil || !unchangedTerminalReport(report, member.ReportInfo) {
		return fmt.Errorf("原始报告已变化")
	}
	names, err := terminalReportDeletionEntries(child)
	if err != nil {
		return err
	}
	return checkTerminalReportEntries(child, member, names)
}

// Validate the whole scope before removing any member. Use directory handles,
// keep batch.json until last, and report each removed raw record on partial error.
func removeTerminalHistory(d *terminalReportDeletion) (deleted []string, complete bool, err error) {
	if !d.History.Batch {
		removed, err := removeTerminalReport(d)
		if removed {
			deleted = append(deleted, d.Run.Path)
		}
		return deleted, removed, err
	}
	parent, err := os.OpenRoot(filepath.Dir(d.Dir))
	if err != nil {
		return nil, false, err
	}
	defer parent.Close()
	name := filepath.Base(d.Dir)
	info, err := parent.Lstat(name)
	if err != nil || !info.IsDir() || !os.SameFile(info, d.DirInfo) {
		return nil, false, fmt.Errorf("运行目录已变化；本次未删除")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	if err := checkTerminalHistoryRoot(root, d); err != nil {
		return nil, false, err
	}
	for _, member := range d.Members {
		if err := checkTerminalHistoryMember(root, member); err != nil {
			return nil, false, fmt.Errorf("%s: %w", member.Run.DisplayName(), err)
		}
	}
	for _, member := range d.Members {
		removed, err := removeTerminalReportAt(root, member)
		if removed {
			deleted = append(deleted, member.Run.Path)
		}
		if err != nil {
			return deleted, false, fmt.Errorf("已删除 %d/%d 份报告：%w", len(deleted), len(d.Members), err)
		}
	}
	batch, err := root.Lstat("batch.json")
	if err != nil || !unchangedTerminalReport(batch, d.BatchInfo) {
		return deleted, false, fmt.Errorf("报告已删除，运行汇总发生变化而保留")
	}
	if err := root.Remove("batch.json"); err != nil {
		return deleted, false, err
	}
	if err := root.Close(); err != nil {
		return deleted, true, err
	}
	info, err = parent.Lstat(name)
	if err != nil || !info.IsDir() || !os.SameFile(info, d.DirInfo) {
		return deleted, true, fmt.Errorf("报告已删除，运行目录位置发生变化")
	}
	if err := parent.Remove(name); err != nil {
		return deleted, true, fmt.Errorf("报告已删除，运行目录有遗留：%w", err)
	}
	return deleted, true, nil
}
