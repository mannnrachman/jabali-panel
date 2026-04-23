package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// autoresponderSetParams is the panel → agent request for autoresponder.set.
//
// Idempotent upsert of VacationResponse singleton on the mailbox's account.
// If Enabled is false, VacationResponse stays present but isEnabled=false
// so the server preserves body+dates for re-enable later.
type autoresponderSetParams struct {
	MailboxEmail string  `json:"mailbox_email"`
	Enabled      bool    `json:"enabled"`
	FromDate     *string `json:"from_date"` // RFC 3339, or nil
	ToDate       *string `json:"to_date"`
	Subject      *string `json:"subject"`
	TextBody     *string `json:"text_body"`
	HTMLBody     *string `json:"html_body"`
}

type autoresponderSetResponse struct {
	Ok bool `json:"ok"`
}

func autoresponderSetHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p autoresponderSetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if _, err := requireEmail(p.MailboxEmail); err != nil {
		return nil, err
	}

	acctID, err := accountIDByEmail(ctx, p.MailboxEmail)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("resolve account: %v", err)}
	}
	if acctID == "" {
		// Account hasn't authed yet; autoresponder can't be set.
		return nil, &agentwire.AgentError{Code: agentwire.CodeNotFound, Message: "mailbox not yet registered with mail server"}
	}

	// VacationResponse is a singleton with fixed id "singleton" per JMAP RFC 8621.
	patch := map[string]any{
		"isEnabled": p.Enabled,
	}
	if p.FromDate != nil {
		patch["fromDate"] = *p.FromDate
	} else {
		patch["fromDate"] = nil
	}
	if p.ToDate != nil {
		patch["toDate"] = *p.ToDate
	} else {
		patch["toDate"] = nil
	}
	if p.Subject != nil {
		patch["subject"] = *p.Subject
	} else {
		patch["subject"] = nil
	}
	if p.TextBody != nil {
		patch["textBody"] = *p.TextBody
	} else {
		patch["textBody"] = nil
	}
	if p.HTMLBody != nil {
		patch["htmlBody"] = *p.HTMLBody
	} else {
		patch["htmlBody"] = nil
	}

	args := map[string]any{
		"accountId": acctID,
		"update": map[string]any{
			"singleton": patch,
		},
	}

	var result jmapSetResult
	if err := jmapCall(ctx, "VacationResponse/set", args, &result); err != nil {
		return nil, err
	}
	if reason, ok := result.NotUpdated["singleton"]; ok {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("VacationResponse/set refused: %s", string(reason))}
	}
	return autoresponderSetResponse{Ok: true}, nil
}

func init() {
	Default.Register("autoresponder.set", autoresponderSetHandler)
}
