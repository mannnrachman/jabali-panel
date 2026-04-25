package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/ntfy"
)

// systemDiagnosticNotifyParams is the operator's "send the link to the
// team" payload. URL + password come from a previous
// system.diagnostic_report call; we wrap them in a friendly ntfy push
// the team's mobile clients will see.
type systemDiagnosticNotifyParams struct {
	URL      string `json:"url"`
	Password string `json:"password"`
	Note     string `json:"note,omitempty"`
}

type systemDiagnosticNotifyResponse struct {
	Ok bool `json:"ok"`
}

func systemDiagnosticNotifyHandler(ctx context.Context, raw json.RawMessage) (any, error) {
	var p systemDiagnosticNotifyParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.URL == "" || p.Password == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "url and password required"}
	}

	hostname, _ := os.Hostname()
	body := fmt.Sprintf("Host: %s\n\nLink: %s\nPassword: %s", hostname, p.URL, p.Password)
	if p.Note != "" {
		body += "\n\nNote from operator:\n" + p.Note
	}

	client := ntfy.NewClient(ntfyBaseURL())
	// Priority 4 = "high" on ntfy clients — pops a notification the
	// team will see promptly. Title surfaces in the push preview.
	if err := client.Publish(ctx, ntfyTopic(),
		fmt.Sprintf("Diagnostic from %s", hostname), body, 4); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	return systemDiagnosticNotifyResponse{Ok: true}, nil
}

func init() {
	Default.Register("system.diagnostic_notify", systemDiagnosticNotifyHandler)
}
