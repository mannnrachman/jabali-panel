package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDomainEnableHandler_InvalidDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"uppercase", "Example.COM"},
		{"starts with hyphen", "-example.com"},
		{"single label", "localhost"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := domainToggleParams{
				Domain: tt.domain,
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := domainEnableHandler(context.Background(), paramsJSON)
			require.NotNil(t, err)

			var aerr *agentwire.AgentError
			require.ErrorAs(t, err, &aerr)
			assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
		})
	}
}

func TestDomainDisableHandler_InvalidDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"uppercase", "Example.COM"},
		{"starts with hyphen", "-example.com"},
		{"single label", "localhost"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := domainToggleParams{
				Domain: tt.domain,
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := domainDisableHandler(context.Background(), paramsJSON)
			require.NotNil(t, err)

			var aerr *agentwire.AgentError
			require.ErrorAs(t, err, &aerr)
			assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
		})
	}
}

func TestDomainToggleHandler_ValidDomains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"simple", "example.com"},
		{"subdomain", "sub.example.com"},
		{"multi-subdomain", "sub.sub.example.com"},
		{"hyphenated", "my-domain.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !domainRegex.MatchString(tt.domain) {
				t.Fatalf("expected %q to be valid", tt.domain)
			}
		})
	}
}

func TestDomainEnableHandler_RequiresRootNginx(t *testing.T) {
	t.Skip("requires root + nginx")

	params := domainToggleParams{
		Domain: "example.com",
	}
	paramsJSON, _ := json.Marshal(params)

	_, err := domainEnableHandler(context.Background(), paramsJSON)
	require.NoError(t, err)
}

func TestDomainDisableHandler_RequiresRootNginx(t *testing.T) {
	t.Skip("requires root + nginx")

	params := domainToggleParams{
		Domain: "example.com",
	}
	paramsJSON, _ := json.Marshal(params)

	_, err := domainDisableHandler(context.Background(), paramsJSON)
	require.NoError(t, err)
}
