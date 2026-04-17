package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPickSort_Allowlist(t *testing.T) {
	allowed := []string{"email", "created_at"}

	tests := []struct {
		name      string
		requested string
		want      string
	}{
		{"empty falls back", "", "created_at"},
		{"exact allowed", "email", "email"},
		{"case insensitive match", "EMAIL", "email"},
		{"not in allowlist -> fallback", "password_hash", "created_at"},
		{"injection attempt -> fallback", "id; DROP TABLE users", "created_at"},
		{"whitespace trimmed", "   email   ", "email"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pickSort(tc.requested, allowed, "created_at")
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPickSort_EmptyFallbackMeansNoOrder(t *testing.T) {
	// If fallback is empty and request is unknown, return empty so the
	// caller skips the ORDER BY clause entirely.
	got := pickSort("unknown", []string{"email"}, "")
	assert.Equal(t, "", got)
}
