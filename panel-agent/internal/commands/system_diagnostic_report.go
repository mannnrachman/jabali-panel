package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/diagnostic"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/enclosed"
)

// EnclosedBaseURL points at the operator-controlled enclosed.cc-compatible
// note-sharing server. Hard-coded so the agent ships with the right
// endpoint baked in. Override via the JABALI_ENCLOSED_URL env if a
// dev wants to point at a local instance.
const defaultEnclosedBaseURL = "https://enclosed.jabali-panel.com"

// NtfyBaseURL + topic are the team's notification surface. Operator
// hits "Send via ntfy" → agent posts the just-minted enclosed URL +
// password to this topic and the team gets a push.
const (
	defaultNtfyBaseURL = "https://ntfy.jabali-panel.com"
	defaultNtfyTopic   = "jabali-admin-alerts"
)

func enclosedBaseURL() string {
	if v := os.Getenv("JABALI_ENCLOSED_URL"); v != "" {
		return v
	}
	return defaultEnclosedBaseURL
}

func ntfyBaseURL() string {
	if v := os.Getenv("JABALI_NTFY_URL"); v != "" {
		return v
	}
	return defaultNtfyBaseURL
}

func ntfyTopic() string {
	if v := os.Getenv("JABALI_NTFY_TOPIC"); v != "" {
		return v
	}
	return defaultNtfyTopic
}

// systemDiagnosticReportResponse mirrors what the panel UI renders on
// /jabali-admin/support after the operator clicks "Send Diagnostic
// Report". The URL + password are independent: the URL alone won't
// decrypt without the password, and the password without the URL is
// useless. The team needs both, so the next step is the operator posting
// them to ntfy via system.diagnostic_notify.
type systemDiagnosticReportResponse struct {
	URL            string `json:"url"`
	Password       string `json:"password"`
	NoteID         string `json:"note_id"`
	NtfyURL        string `json:"ntfy_url"`
	NtfyTopic      string `json:"ntfy_topic"`
	ByteCount      int    `json:"byte_count"`
	GeneratedAt    string `json:"generated_at"`
	RedactionCount int    `json:"redaction_count"`
	FileCount      int    `json:"file_count"`
}

func systemDiagnosticReportHandler(ctx context.Context, _ json.RawMessage) (any, error) {
	bundle, err := diagnostic.Build(ctx)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}

	hostname, _ := os.Hostname()
	fileName := fmt.Sprintf("jabali-diagnostic-%s-%s.tar",
		safeHostname(hostname),
		bundle.GeneratedAt.Format("2006-01-02-150405"))

	client := enclosed.NewClient(enclosedBaseURL())
	// 7-day TTL — long enough for the team to read at their leisure,
	// short enough that stale ciphertext rotates out without manual
	// cleanup.
	const ttlSeconds = 7 * 24 * 3600
	res, err := client.UploadFile(ctx, fileName, "application/x-tar", bundle.TarBytes, ttlSeconds)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("enclosed upload: %v", err)}
	}

	return systemDiagnosticReportResponse{
		URL:            res.URL,
		Password:       res.Password,
		NoteID:         res.NoteID,
		NtfyURL:        ntfyBaseURL() + "/" + ntfyTopic(),
		NtfyTopic:      ntfyTopic(),
		ByteCount:      len(bundle.TarBytes),
		GeneratedAt:    bundle.GeneratedAt.Format(time.RFC3339),
		RedactionCount: bundle.RedactionCount,
		FileCount:      bundle.FileCount,
	}, nil
}

// safeHostname strips characters that would mess up a filename; falls
// back to "host" when the hostname is empty (containers with HOSTNAME
// unset).
func safeHostname(h string) string {
	if h == "" {
		return "host"
	}
	out := make([]rune, 0, len(h))
	for _, r := range h {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			out = append(out, r)
		} else {
			out = append(out, '-')
		}
	}
	return string(out)
}

func init() {
	Default.Register("system.diagnostic_report", systemDiagnosticReportHandler)
}
