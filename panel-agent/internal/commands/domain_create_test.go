package commands

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestDomainCreateHandler_InvalidDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"uppercase", "Example.COM"},
		{"starts with hyphen", "-example.com"},
		{"ends with hyphen", "example-.com"},
		{"single label", "localhost"},
		{"no tld", "example"},
		{"dots only", "."},
		{"dot at start", ".example.com"},
		{"dot at end", "example.com."},
		{"double dot", "example..com"},
		{"spaces", "exam ple.com"},
		{"special chars", "exam@ple.com"},
		{"underscore", "exam_ple.com"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := domainCreateParams{
				Username:   "testuser",
				Domain:     tt.domain,
				DocRoot:    "/home/testuser/public_html/example.com",
				PHPVersion: "8.3",
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := domainCreateHandler(context.Background(), paramsJSON)
			require.NotNil(t, err)

			var aerr *agentwire.AgentError
			require.ErrorAs(t, err, &aerr)
			assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
		})
	}
}

func TestDomainCreateHandler_ValidDomains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"simple", "example.com"},
		{"subdomain", "sub.example.com"},
		{"multi-subdomain", "sub.sub.example.com"},
		{"hyphenated", "my-domain.com"},
		{"numbers", "example123.com"},
		{"start with number", "1example.com"},
		{"co.uk", "example.co.uk"},
		{"many hyphens", "my-long-domain-name.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just test that validation passes (we skip actual file system operations)
			if !domainRegex.MatchString(tt.domain) {
				t.Fatalf("expected %q to be valid", tt.domain)
			}
		})
	}
}

func TestDomainCreateHandler_InvalidUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
	}{
		{"uppercase", "TestUser"},
		{"starts with digit", "0user"},
		{"special char", "user@name"},
		{"too long", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := domainCreateParams{
				Username:   tt.username,
				Domain:     "example.com",
				DocRoot:    "/home/testuser/public_html/example.com",
				PHPVersion: "8.3",
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := domainCreateHandler(context.Background(), paramsJSON)
			require.NotNil(t, err)

			var aerr *agentwire.AgentError
			require.ErrorAs(t, err, &aerr)
			assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
		})
	}
}

func TestDomainCreateHandler_InvalidDocRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		docRoot string
	}{
		{"missing /home prefix", "/root/public_html/example.com"},
		{"relative path", "public_html/example.com"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := domainCreateParams{
				Username:   "testuser",
				Domain:     "example.com",
				DocRoot:    tt.docRoot,
				PHPVersion: "8.3",
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := domainCreateHandler(context.Background(), paramsJSON)
			require.NotNil(t, err)

			var aerr *agentwire.AgentError
			require.ErrorAs(t, err, &aerr)
			assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
		})
	}
}

func TestDomainCreateHandler_RequiresRootNginx(t *testing.T) {
	t.Skip("requires root + nginx")

	params := domainCreateParams{
		Username:   "testuser",
		Domain:     "example.com",
		DocRoot:    "/home/testuser/public_html/example.com",
		PHPVersion: "8.3",
	}
	paramsJSON, _ := json.Marshal(params)

	_, err := domainCreateHandler(context.Background(), paramsJSON)
	require.NoError(t, err)
}
