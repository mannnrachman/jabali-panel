package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/certbot"
)

// sslRevokeParams is the input shape for ssl.revoke.
type sslRevokeParams struct {
	Domain string `json:"domain"`
	Reason string `json:"reason"`
}

// sslRevokeResponse is the output shape for ssl.revoke.
type sslRevokeResponse struct {
	Revoked bool `json:"revoked"`
}

func sslRevokeHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p sslRevokeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}

	// Validate domain format
	if !sslDomainRegex.MatchString(p.Domain) {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid domain %q: must match ^[a-zA-Z0-9][a-zA-Z0-9.-]{1,253}$", p.Domain),
		}
	}

	// Validate revoke reason if provided
	validReasons := map[string]bool{
		"":                     true,
		"unspecified":          true,
		"keycompromise":        true,
		"affiliationchanged":   true,
		"superseded":           true,
		"cessationofoperation": true,
	}
	if !validReasons[p.Reason] {
		return nil, &agentwire.AgentError{
			Code: agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("invalid reason %q: must be one of unspecified, keycompromise, affiliationchanged, superseded, cessationofoperation", p.Reason),
		}
	}

	// Run certbot revoke
	runner := certbot.NewRunner()
	_, err := runner.Revoke(p.Domain, p.Reason)

	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("certbot revoke failed: %v", err),
		}
	}

	return sslRevokeResponse{
		Revoked: true,
	}, nil
}

func init() {
	Default.Register("ssl.revoke", sslRevokeHandler)
}
