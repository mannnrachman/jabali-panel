package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestSSLPanelIssueValidation_InvalidHostname(t *testing.T) {
	cases := []struct {
		name string
		host string
	}{
		{"empty", ""},
		{"slash injection", "panel.example.com/../"},
		{"space inside", "panel example.com"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(sslPanelIssueParams{
				Hostname: tc.host,
				Email:    "admin@example.com",
			})
			res, err := sslPanelIssueHandler(context.Background(), body)
			if err == nil {
				t.Fatalf("expected error for hostname %q", tc.host)
			}
			if res != nil {
				t.Fatalf("expected nil result, got %v", res)
			}
			ae, ok := err.(*agentwire.AgentError)
			if !ok {
				t.Fatalf("expected AgentError, got %T", err)
			}
			if ae.Code != agentwire.CodeInvalidArgument {
				t.Errorf("expected CodeInvalidArgument, got %s", ae.Code)
			}
		})
	}
}

func TestSSLPanelIssueValidation_InvalidExtraHostname(t *testing.T) {
	body, _ := json.Marshal(sslPanelIssueParams{
		Hostname:       "panel.example.com",
		ExtraHostnames: []string{"mail panel.example.com"},
		Email:          "admin@example.com",
	})
	_, err := sslPanelIssueHandler(context.Background(), body)
	if err == nil {
		t.Fatal("expected error for malformed extra hostname")
	}
	ae, ok := err.(*agentwire.AgentError)
	if !ok || ae.Code != agentwire.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestSSLPanelIssueValidation_InvalidEmail(t *testing.T) {
	body, _ := json.Marshal(sslPanelIssueParams{
		Hostname: "panel.example.com",
		Email:    "not-an-email",
	})
	_, err := sslPanelIssueHandler(context.Background(), body)
	if err == nil {
		t.Fatal("expected error for invalid email")
	}
	ae, ok := err.(*agentwire.AgentError)
	if !ok || ae.Code != agentwire.CodeInvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestSSLPanelIssue_MissingWebroot(t *testing.T) {
	// Inputs all valid; webroot dir does not exist on the test host
	// (CI doesn't run install.sh) so the handler should return
	// FailedPrecondition before touching certbot.
	body, _ := json.Marshal(sslPanelIssueParams{
		Hostname: "panel.example.com",
		Email:    "admin@example.com",
	})
	_, err := sslPanelIssueHandler(context.Background(), body)
	if err == nil {
		t.Fatal("expected error when webroot missing")
	}
	ae, ok := err.(*agentwire.AgentError)
	if !ok {
		t.Fatalf("expected AgentError, got %T", err)
	}
	// The webroot is the most likely missing precondition in CI; if
	// certbot is actually installed the handler can also fail at the
	// certbot invocation. Either FailedPrecondition (webroot) or
	// Internal (certbot) is acceptable here — we just want a
	// non-success code.
	if ae.Code != agentwire.CodeFailedPrecondition && ae.Code != agentwire.CodeInternal {
		t.Errorf("expected FailedPrecondition or Internal, got %s", ae.Code)
	}
}
