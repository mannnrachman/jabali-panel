package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailboxUsageParams is the request shape for mailbox.usage.
//
// Unlike the four no-op commands, this one READS from Stalwart's
// registry via JMAP — it's how the reconciler's 5-minute sampler
// keeps mailboxes.last_usage_* columns fresh for the UI's progress
// bar.
type mailboxUsageParams struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// mailboxUsageResponse is the wire contract consumed by the reconciler.
// Panel maps used_bytes -> mailboxes.last_usage_bytes + updates
// last_usage_at = wall-clock now() (not any server-side last-used-at).
//
// Wire contract shape unchanged from Step 3's v0.15 draft so the
// panel-side sampler and golden fixtures don't need updating. In v0.16,
// though, two of the three fields are pinned at zero/empty (see
// mailbox_jmap.go's accountQuotaView schema-gap note):
//
//   - UsedBytes: populated from x:UserAccount.usedDiskQuota
//   - MessageCount: always 0 (no equivalent JMAP property in v0.16)
//   - LastUsedAt:   always "" (no equivalent JMAP property in v0.16)
//
// Panel UI treats MessageCount=0 as "unknown/not shown" and never
// relied on LastUsedAt pre-v0.16 anyway. A future Stalwart release that
// exposes message counts or last-auth timestamps flips these back on
// with a single-field accountQuotaView addition.
type mailboxUsageResponse struct {
	UsedBytes    uint64 `json:"used_bytes"`
	MessageCount uint64 `json:"message_count"`
	// LastUsedAt kept in the shape for wire-contract stability even
	// though v0.16 can't populate it. omitempty keeps the golden
	// fixtures clean.
	LastUsedAt string `json:"last_used_at,omitempty"`
}

func mailboxUsageHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p mailboxUsageParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.ID == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "id parameter required"}
	}
	email, err := requireEmail(p.Email)
	if err != nil {
		return nil, err
	}

	// Step 1: resolve the registry Account id. Lazy-sync means the
	// record won't exist until the mailbox owner has authenticated at
	// least once; for a brand-new mailbox the sampler sees no usage,
	// which maps to "all zeros" — not an error.
	accountID, err := accountIDByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if accountID == "" {
		return mailboxUsageResponse{}, nil
	}

	// Step 2: read disk usage. MessageCount + LastUsedAt stay zero —
	// v0.16 doesn't expose equivalent JMAP properties (see
	// mailbox_jmap.go's accountQuotaView comment for the schema grep).
	info, err := accountQuota(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return mailboxUsageResponse{
		UsedBytes: info.UsedDiskQuota,
	}, nil
}

func init() {
	Default.Register("mailbox.usage", mailboxUsageHandler)
}
