package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// mailLogsQueryParams — panel → agent. Pass-through to Stalwart x:Trace/query.
// Filters are applied server-side: from_date, to_date, sender_prefix, recipient_prefix.
type mailLogsQueryParams struct {
	FromDate        *string  `json:"from_date"`        // RFC 3339
	ToDate          *string  `json:"to_date"`
	SenderPrefix    *string  `json:"sender_prefix"`
	RecipientPrefix *string  `json:"recipient_prefix"`
	DomainNames     []string `json:"domain_names"` // scope — recipient or sender must match ONE of these
	Limit           int      `json:"limit"`
	Offset          int      `json:"offset"`
}

type mailLogEntry struct {
	Timestamp string `json:"timestamp"`
	From      string `json:"from"`
	To        string `json:"to"`
	Size      int    `json:"size"`
}

type mailLogsQueryResponse struct {
	Entries []mailLogEntry `json:"entries"`
	Total   int            `json:"total"`
}

func mailLogsQueryHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p mailLogsQueryParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
		}
	}
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	filter := map[string]any{}
	if p.FromDate != nil {
		filter["after"] = *p.FromDate
	}
	if p.ToDate != nil {
		filter["before"] = *p.ToDate
	}
	if p.SenderPrefix != nil {
		filter["from"] = *p.SenderPrefix
	}
	if p.RecipientPrefix != nil {
		filter["to"] = *p.RecipientPrefix
	}

	args := map[string]any{
		"filter": filter,
		"sort": []map[string]any{
			{"property": "timestamp", "isAscending": false},
		},
		"limit":    p.Limit,
		"position": p.Offset,
	}

	var qResult struct {
		IDs   []string `json:"ids"`
		Total int      `json:"total"`
	}
	if err := jmapCall(ctx, "x:Trace/query", args, &qResult); err != nil {
		// x:Trace may be absent on older Stalwart; fall through empty.
		return mailLogsQueryResponse{Entries: []mailLogEntry{}, Total: 0}, nil
	}

	if len(qResult.IDs) == 0 {
		return mailLogsQueryResponse{Entries: []mailLogEntry{}, Total: qResult.Total}, nil
	}

	getArgs := map[string]any{
		"ids":        qResult.IDs,
		"properties": []string{"timestamp", "from", "to", "size"},
	}
	var getResult struct {
		List []mailLogEntry `json:"list"`
	}
	if err := jmapCall(ctx, "x:Trace/get", getArgs, &getResult); err != nil {
		return mailLogsQueryResponse{Entries: []mailLogEntry{}, Total: qResult.Total}, nil
	}

	// Scope filter: only return entries whose from or to matches one of DomainNames.
	if len(p.DomainNames) > 0 {
		filtered := make([]mailLogEntry, 0, len(getResult.List))
		for _, e := range getResult.List {
			for _, d := range p.DomainNames {
				if containsDomain(e.From, d) || containsDomain(e.To, d) {
					filtered = append(filtered, e)
					break
				}
			}
		}
		return mailLogsQueryResponse{Entries: filtered, Total: len(filtered)}, nil
	}

	return mailLogsQueryResponse{Entries: getResult.List, Total: qResult.Total}, nil
}

func containsDomain(addr, domain string) bool {
	if addr == "" || domain == "" {
		return false
	}
	for i := len(addr) - 1; i > 0; i-- {
		if addr[i] == '@' {
			return addr[i+1:] == domain
		}
	}
	return false
}

func init() {
	Default.Register("mail.logs_query", mailLogsQueryHandler)
}
