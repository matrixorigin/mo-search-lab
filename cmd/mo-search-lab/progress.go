// Copyright 2026 Matrix Origin
// Licensed under the Apache License, Version 2.0.
package main

import (
	"context"
	"fmt"
	"os"
)

type progressKey struct{}

// Progress is emitted between measured batches, never by query workers.
func notifyProgress(ctx context.Context, message string) {
	if observer, ok := ctx.Value(progressKey{}).(func(string)); ok {
		observer(message)
	}
}

func logProgress(ctx context.Context, message string) {
	if _, ok := ctx.Value(progressKey{}).(func(string)); ok {
		notifyProgress(ctx, message)
	} else {
		fmt.Fprintln(os.Stderr, message)
	}
}

type contextReader struct {
	ctx  context.Context
	read func([]byte) (int, error)
}

func (r contextReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.read(body)
}
