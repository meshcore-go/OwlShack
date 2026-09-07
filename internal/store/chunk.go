package store

import (
	"context"
	"fmt"
	"strings"
)

// eachInChunk splits a pubkey list into batches under SQLite's bound-parameter limit, calling fn with the placeholders and args for each.
func eachInChunk(ctx context.Context, pubkeys [][]byte, fn func(ctx context.Context, placeholders string, args []any) error) error {
	const chunk = 500
	for start := 0; start < len(pubkeys); start += chunk {
		end := start + chunk
		if end > len(pubkeys) {
			end = len(pubkeys)
		}
		batch := pubkeys[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, pk := range batch {
			args[i] = pk
		}
		if err := fn(ctx, placeholders, args); err != nil {
			return fmt.Errorf("processing pubkey chunk: %w", err)
		}
	}
	return nil
}
