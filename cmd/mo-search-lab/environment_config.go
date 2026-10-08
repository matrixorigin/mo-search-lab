// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
package main

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type MOConfigEvidence struct {
	File         fileRef             `json:"file"`
	ServiceType  string              `json:"service_type,omitempty"`
	NodeID       string              `json:"node_id,omitempty"`
	FileServices []FileServiceConfig `json:"file_services,omitempty"`
	Memory       []ConfiguredMemory  `json:"memory,omitempty"`
	Meaning      string              `json:"meaning"`
}

type FileServiceConfig struct {
	Name           string           `json:"name"`
	Backend        string           `json:"backend,omitempty"`
	MemoryCapacity ConfiguredMemory `json:"memory_capacity"`
	DiskCapacity   ConfiguredMemory `json:"disk_capacity"`
	DiskPath       string           `json:"disk_path,omitempty"`
	// Decode older report records; new captures do not collect this field.
	RemoteCache *bool `json:"remote_cache_enabled,omitempty"`
}

type ConfiguredMemory struct {
	Key    string `json:"key"`
	Raw    string `json:"raw,omitempty"`
	Bytes  *int64 `json:"bytes,omitempty"`
	Status string `json:"status"`
}

// Only retain selected metadata and sizing fields. Storage credentials and the
// complete TOML content never enter either report or the environment sidecar.
func readMOConfig(path string) (MOConfigEvidence, error) {
	data, file, err := readEvidenceFile(path)
	if err != nil {
		return MOConfigEvidence{}, err
	}
	var document map[string]any
	if err := toml.Unmarshal(data, &document); err != nil {
		// Decoder errors can include raw configuration lines. Do not serialize them.
		return MOConfigEvidence{}, fmt.Errorf("invalid TOML in %s", file.Path)
	}
	if len(document) == 0 {
		return MOConfigEvidence{}, fmt.Errorf("MO config %s is empty", file.Path)
	}
	config := MOConfigEvidence{File: file, Meaning: "explicit file values only; runtime loading, defaults and live utilization are unverified"}
	config.ServiceType, _ = configValue(document, "service-type").(string)
	for _, service := range []string{"cn", "tn", "dn", "logservice", "log", "proxy"} {
		if value, ok := configValue(document, service+".uuid").(string); ok && value != "" {
			config.NodeID = value
			break
		}
	}
	if list, ok := configValue(document, "fileservice").([]any); ok {
		if len(list) > 64 {
			return MOConfigEvidence{}, fmt.Errorf("MO config has more than 64 FileServices")
		}
		for i, item := range list {
			service, ok := item.(map[string]any)
			if !ok {
				return MOConfigEvidence{}, fmt.Errorf("invalid FileService table")
			}
			name, _ := configValue(service, "name").(string)
			if excludedFileService(name) {
				continue
			}
			backend, _ := configValue(service, "backend").(string)
			prefix := fmt.Sprintf("fileservice[%d].cache.", i)
			memory := configuredMemory(prefix+"memory-capacity", configValue(service, "cache.memory-capacity"))
			disk := configuredMemory(prefix+"disk-capacity", configValue(service, "cache.disk-capacity"))
			diskPath, _ := configValue(service, "cache.disk-path").(string)
			entry := FileServiceConfig{Name: name, Backend: backend, MemoryCapacity: memory, DiskCapacity: disk, DiskPath: diskPath}
			config.FileServices = append(config.FileServices, entry)
		}
	} else if configValue(document, "fileservice") != nil {
		return MOConfigEvidence{}, fmt.Errorf("fileservice must use TOML array tables [[fileservice]]")
	}
	config.Memory = append(config.Memory, configuredMemory("metacache.memory-capacity", configValue(document, "metacache.memory-capacity")))
	for _, key := range []string{"cn.pipeline.host-size", "cn.pipeline.guest-size", "cn.pipeline.batch-size", "cn.frontend.mempoolMaxSize"} {
		if value := configValue(document, key); value != nil {
			config.Memory = append(config.Memory, configuredMemory(key, value))
		}
	}
	return config, nil
}

func configValue(document map[string]any, path string) any {
	var current any = document
	for _, part := range strings.Split(path, ".") {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = nil
		// Resolve exact spelling first; never choose ambiguous case-folded keys.
		if value, ok := mapping[part]; ok {
			current = value
			continue
		}
		keys := make([]string, 0, len(mapping))
		for key := range mapping {
			if strings.EqualFold(key, part) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if len(keys) != 1 {
			return nil
		}
		current = mapping[keys[0]]
	}
	return current
}

func configuredMemory(key string, value any) ConfiguredMemory {
	result := ConfiguredMemory{Key: key, Status: "not_set"}
	if value == nil {
		return result
	}
	result.Status, result.Raw = "unrecognized", fmt.Sprint(value)
	if len(result.Raw) > 256 {
		result.Raw = "value exceeds 256 bytes"
		return result
	}
	var bytes int64
	var valid bool
	switch value := value.(type) {
	case int64:
		bytes, valid = value, value >= 0
	case string:
		bytes, valid = parseMOByteSize(value)
	}
	if valid {
		result.Bytes, result.Status = &bytes, "configured"
	}
	return result
}

var moByteSizePattern = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)\s*([kmgtpe]?)(?:i?b)?$`)

// MatrixOne's TOML ByteSize uses RAMInBytes: MB and MiB both mean 1024^2.
// Use exact decimal arithmetic and truncate fractional bytes like RAMInBytes.
func parseMOByteSize(raw string) (int64, bool) {
	match := moByteSizePattern.FindStringSubmatch(strings.TrimSpace(raw))
	if match == nil {
		return 0, false
	}
	value, ok := new(big.Rat).SetString(match[1])
	if !ok {
		return 0, false
	}
	power := 0
	if match[2] != "" {
		power = strings.Index("kmgtpe", strings.ToLower(match[2])) + 1
	}
	factor := new(big.Int).Exp(big.NewInt(1024), big.NewInt(int64(power)), nil)
	value.Mul(value, new(big.Rat).SetInt(factor))
	integer := new(big.Int).Quo(value.Num(), value.Denom())
	if !integer.IsInt64() || integer.Sign() < 0 || integer.Cmp(big.NewInt(math.MaxInt64)) > 0 {
		return 0, false
	}
	return integer.Int64(), true
}
