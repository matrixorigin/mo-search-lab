// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type environmentRow struct{ Label, Value, Source string }
type resourcePlot struct {
	Name, Unit, Status, Error string
	Lines                     []resourceLine
	Maximum, Minimum          float64
	Duration                  float64
}
type resourceLine struct {
	Label, Path, Color     string
	Dots                   []resourceDot
	Samples                int
	Minimum, Mean, Maximum float64
}

type resourceDot struct{ X, Y float64 }

func unknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "未知"
	}
	return value
}
func metricSeriesLabel(labels map[string]string, i int) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if key != "__name__" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	if len(parts) == 0 {
		return fmt.Sprintf("series %d", i+1)
	}
	return strings.Join(parts, ", ")
}

func buildResourcePlots(e *EnvironmentEvidence) []resourcePlot {
	if e == nil {
		return nil
	}
	var plots []resourcePlot
	colors := []string{"#1565c0", "#00897b", "#b25f00", "#8b3bb5", "#b23448", "#526273"}
	for _, metric := range e.Monitoring.Metrics {
		plot := resourcePlot{Name: metric.Name, Unit: metric.Unit, Status: metric.Status, Error: metric.Error}
		low, high, first, last := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
		for _, series := range metric.Series {
			for _, point := range series.Samples {
				if point.Value == nil || math.IsNaN(*point.Value) || math.IsInf(*point.Value, 0) || math.IsNaN(point.Timestamp) || math.IsInf(point.Timestamp, 0) {
					continue
				}
				low, high = math.Min(low, *point.Value), math.Max(high, *point.Value)
				first, last = math.Min(first, point.Timestamp), math.Max(last, point.Timestamp)
			}
		}
		if math.IsInf(low, 1) {
			plots = append(plots, plot)
			continue
		}
		if e.Monitoring.WindowStart != nil && e.Monitoring.WindowEnd != nil && !e.Monitoring.WindowEnd.Before(*e.Monitoring.WindowStart) {
			first, last = float64(e.Monitoring.WindowStart.UnixNano())/1e9, float64(e.Monitoring.WindowEnd.UnixNano())/1e9
		}
		plot.Minimum, plot.Maximum, plot.Duration = low, high, last-first
		axisLow, axisHigh := math.Min(0, low), math.Max(0, high)
		if axisLow == axisHigh {
			axisHigh = axisLow + 1
		}
		scale := math.Max(math.Abs(axisLow), math.Abs(axisHigh))
		for i, series := range metric.Series {
			line := resourceLine{Label: metricSeriesLabel(series.Labels, i), Color: colors[i%len(colors)], Minimum: math.Inf(1), Maximum: math.Inf(-1)}
			var path strings.Builder
			connected := false
			segmentPoints := 0
			var previous resourceDot
			for _, point := range series.Samples {
				if point.Value == nil || math.IsNaN(*point.Value) || math.IsInf(*point.Value, 0) || math.IsNaN(point.Timestamp) || math.IsInf(point.Timestamp, 0) {
					if segmentPoints == 1 {
						line.Dots = append(line.Dots, previous)
					}
					connected, segmentPoints = false, 0
					continue
				}
				x := 40.0
				if last > first {
					x += (point.Timestamp - first) / (last - first) * 680
				}
				y := 155 - (*point.Value/scale-axisLow/scale)/(axisHigh/scale-axisLow/scale)*125
				command := "M"
				if connected {
					command = "L"
				}
				fmt.Fprintf(&path, "%s%.2f %.2f ", command, x, y)
				connected = true
				segmentPoints++
				previous = resourceDot{x, y}
				line.Samples++
				// Online mean avoids overflowing sums for otherwise finite samples.
				line.Mean = line.Mean*(float64(line.Samples-1)/float64(line.Samples)) + *point.Value/float64(line.Samples)
				line.Minimum, line.Maximum = math.Min(line.Minimum, *point.Value), math.Max(line.Maximum, *point.Value)
			}
			if segmentPoints == 1 {
				line.Dots = append(line.Dots, previous)
			}
			if line.Samples > 0 {
				line.Path = path.String()
				plot.Lines = append(plot.Lines, line)
			}
		}
		plots = append(plots, plot)
	}
	return plots
}

const inspectionBody = `<body><main><header class="report-header"><h1>MO Search Lab · 运行环境检查</h1><p class="lead">{{.StartedAt.Format "2006-01-02 15:04:05 UTC"}} · MO {{.MatrixOneVersion}} · {{if eq .Status "passed"}}<strong class="pass">SQL 入口可访问</strong>{{else}}<strong class="fail">检查失败</strong>{{end}}</p><p>SQL 入口：<code>{{.Profile.SQLAddress}}</code> · 本次只读环境信息，未执行性能负载。</p></header><!-- environment -->{{if .Errors}}<section class="card"><h2>检查错误</h2>{{range .Errors}}<p class="fail">{{.}}</p>{{end}}</section>{{end}}<p class="environment-footer">工具 {{.ToolVersion}}{{if ne .RendererVersion .ToolVersion}} · 展示 {{.RendererVersion}}{{end}}</p></main></body></html>`
