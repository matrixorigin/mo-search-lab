// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Optional presentation metadata is bounded and stays within the pack root.
func previewPackJSON(root, name string, limit int64, target any) bool {
	if !strings.HasSuffix(name, ".json") {
		return false
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	path, err := (&pack{Root: absolute}).safePath(name)
	if err != nil {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(f, limit+1))
	return err == nil && int64(len(body)) <= limit && json.Unmarshal(body, target) == nil
}
