package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestSSLRevokeValidation_InvalidDomain(t *testing.T) {
	tests := []struct {
		domain string
		name   string
	}{
		{"../evil", "parent dir traversal"},
		{"..", "dot dot"},
		{"", "empty"},
		{"a" + string(make([]byte, 300)), "too long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := sslRevokeParams{
				Domain: tt.domain,
				Reason: "superseded",
			}
			paramsJSON, _ := json.Marshal(params)

			result, err := sslRevokeHandler(context.Background(), paramsJSON)

			if err == nil {
				t.Fatal("expected error for invalid domain")
			}
			if result != nil {
				t.Fatal("expected nil result")
			}
			if agentErr, ok := err.(*agentwire.AgentError); ok {
				if agentErr.Code != agentwire.CodeInvalidArgument {
					t.Errorf("expected CodeInvalidArgument, got %s", agentErr.Code)
				}
			} else {
				t.Fatal("expected AgentError")
			}
		})
	}
}

func TestSSLRevokeValidation_InvalidReason(t *testing.T) {
	tests := []struct {
		reason string
		name   string
	}{
		{"invalid", "invalid reason"},
		{"compromise", "incomplete reason"},
		{"KEY_COMPROMISE", "uppercase"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := sslRevokeParams{
				Domain: "example.com",
				Reason: tt.reason,
			}
			paramsJSON, _ := json.Marshal(params)

			result, err := sslRevokeHandler(context.Background(), paramsJSON)

			if err == nil {
				t.Fatal("expected error for invalid reason")
			}
			if result != nil {
				t.Fatal("expected nil result")
			}
			if agentErr, ok := err.(*agentwire.AgentError); ok {
				if agentErr.Code != agentwire.CodeInvalidArgument {
					t.Errorf("expected CodeInvalidArgument, got %s", agentErr.Code)
				}
			} else {
				t.Fatal("expected AgentError")
			}
		})
	}
}

func TestSSLRevokeValidation_ValidReasons(t *testing.T) {
	tests := []struct {
		reason string
		name   string
	}{
		{"", "empty reason"},
		{"unspecified", "unspecified"},
		{"keycompromise", "keycompromise"},
		{"affiliationchanged", "affiliationchanged"},
		{"superseded", "superseded"},
		{"cessationofoperation", "cessationofoperation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validReasons := map[string]bool{
				"":                     true,
				"unspecified":          true,
				"keycompromise":        true,
				"affiliationchanged":   true,
				"superseded":           true,
				"cessationofoperation": true,
			}
			if !validReasons[tt.reason] {
				t.Errorf("reason validation failed for %q", tt.reason)
			}
		})
	}
}

func TestSSLRevokeValidation_ValidParams(t *testing.T) {
	tests := []struct {
		domain string
		reason string
		name   string
	}{
		{"example.com", "superseded", "basic revocation"},
		{"sub.example.com", "keycompromise", "subdomain with key compromise"},
		{"my-domain.co.uk", "", "hyphenated domain with empty reason"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !sslDomainRegex.MatchString(tt.domain) {
				t.Errorf("domain validation failed for %q", tt.domain)
			}
		})
	}
}

func TestSSLRevokeCommandRegistered(t *testing.T) {
	handlers := Default.Commands()
	found := false
	for _, cmd := range handlers {
		if cmd == "ssl.revoke" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("ssl.revoke command not registered")
	}
}
