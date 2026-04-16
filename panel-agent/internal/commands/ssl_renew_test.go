package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestSSLRenewValidation_InvalidDomain(t *testing.T) {
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
			params := sslRenewParams{
				Domain: tt.domain,
				Force:  false,
			}
			paramsJSON, _ := json.Marshal(params)

			result, err := sslRenewHandler(context.Background(), paramsJSON)

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

func TestSSLRenewValidation_ValidParams(t *testing.T) {
	tests := []struct {
		domain string
		force  bool
		name   string
	}{
		{"example.com", false, "basic renewal"},
		{"sub.example.com", true, "subdomain with force"},
		{"my-domain.co.uk", false, "hyphenated domain"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !sslDomainRegex.MatchString(tt.domain) {
				t.Errorf("domain validation failed for %q", tt.domain)
			}
		})
	}
}

func TestSSLRenewCommandRegistered(t *testing.T) {
	handlers := Default.Commands()
	found := false
	for _, cmd := range handlers {
		if cmd == "ssl.renew" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("ssl.renew command not registered")
	}
}
