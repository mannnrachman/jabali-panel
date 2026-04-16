package main

import (
	"encoding/json"
	"fmt"
)

// printJSON marshals v with indentation and prints to stdout.
func printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("json encode: %w", err)
	}
	fmt.Println(string(b))
	return nil
}

// boolPtr returns a pointer to a bool value.
func boolPtr(b bool) *bool {
	return &b
}
