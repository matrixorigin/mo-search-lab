// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"flag"
	"io"
	"strings"
	"testing"
)

func TestDirectPasswordFlagsAndUISeed(t *testing.T) {
	t.Setenv("MO_BENCH_PASSWORD", "ignored-legacy-password")
	for _, command := range []string{"run", "inspect", "ui"} {
		for _, password := range []string{"", " spaced-q-$'中文 "} {
			fs := flag.NewFlagSet(command, flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			o := defaultTerminalOptions()
			if command == "run" {
				registerRunFlags(fs, &o)
			} else {
				registerConnectionFlags(fs, &o)
			}
			if err := fs.Parse([]string{"--password", password}); err != nil {
				t.Fatal(err)
			}
			if got := makeSQLConfig(o).Passwd; got != password {
				t.Fatal(command, "password changed or read from environment")
			}
			m := launchTestModel(t)
			m.launchSeed = o
			m.openLauncher()
			if m.launcher.fields[launchPassword].Value != password {
				t.Fatal("command password did not pre-fill UI")
			}
			if password != "" && strings.Contains(m.View(), password) {
				t.Fatal("UI displayed command password")
			}
			request, err := m.launcher.request(m.launchSeed)
			if err != nil || makeSQLConfig(request.Options).Passwd != password {
				t.Fatal("UI altered command password", err)
			}
			m.closeTask()
			if m.launchSeed.password != "" || m.launcher.fields[launchPassword].Value != "" {
				t.Fatal("closed UI retained password fields")
			}
		}
	}
}
