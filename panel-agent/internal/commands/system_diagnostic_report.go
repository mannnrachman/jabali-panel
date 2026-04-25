package commands

import (
	"context"
	"encoding/json"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/diagnostic"
)

// systemDiagnosticReportHandler runs the host collector, redacts every
// blob, and encrypts the bundle to the team's age recipient. The
// returned ciphertext is safe to paste in a public GitHub issue —
// redaction has already stripped credentials so a future private-key
// compromise can't turn old reports into a credential dump (ADR-0064).
func systemDiagnosticReportHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	files := diagnostic.Collect(ctx)
	report, err := diagnostic.Encrypt(files, diagnostic.RecipientPublicKey)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	return report, nil
}

func init() {
	Default.Register("system.diagnostic_report", systemDiagnosticReportHandler)
}
