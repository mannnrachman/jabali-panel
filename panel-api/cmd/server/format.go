package main

import (
	"encoding/json"
	"fmt"
)

// derefStr returns *s or empty when nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// printJSON marshals v with indentation and prints to stdout.
func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("json encode: %w", err)
	}
	fmt.Println(string(b))
	return nil
}
