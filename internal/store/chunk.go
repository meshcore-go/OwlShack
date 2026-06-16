package store

import "strings"

// eachInChunk splits a pubkey list into batches that stay under SQLite's
// bound-parameter limit and invokes fn with the "?,?,..." placeholder string
// and matching args for each batch. Shared by the IN(...) queries over pubkey
// lists (peer delete, contact membership lookup).
func eachInChunk(pubkeys [][]byte, fn func(placeholders string, args []any) error) error {
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
		if err := fn(placeholders, args); err != nil {
			return err
		}
	}
	return nil
}
