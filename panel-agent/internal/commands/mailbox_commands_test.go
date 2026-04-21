package commands

import (
	"context"
	"encoding/json"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// --- no-op commands (create, set_quota, set_password) ----------------
//
// In v0.16 (ADR-0045) these three commands are agent-side no-ops with
// respect to Stalwart. Param validation still runs; no JMAP/HTTP is
// issued. Tests here pin those two properties: (1) happy path acks
// without contacting a server, and (2) param validation rejects bad
// inputs with CodeInvalidArgument.

func TestMailboxNoOpCommands_HappyAndValidation(t *testing.T) {
	type cmd struct {
		name       string
		handler    func(context.Context, json.RawMessage) (any, error)
		goodParams string
	}
	cmds := []cmd{
		{"mailbox.create", mailboxCreateHandler, `{"id":"01J","email":"alice@example.com"}`},
		{"mailbox.set_quota", mailboxSetQuotaHandler, `{"id":"01J","email":"alice@example.com","quota_bytes":1024}`},
		{"mailbox.set_password", mailboxSetPasswordHandler, `{"id":"01J","email":"alice@example.com"}`},
	}
	for _, c := range cmds {
		c := c
		// No server wired: these commands MUST NOT reach out to
		// Stalwart. If they do, the loopback URL won't resolve and
		// the test will error — that's exactly what we want to
		// catch.
		t.Run(c.name+"/happy-without-server", func(t *testing.T) {
			got, err := c.handler(context.Background(), json.RawMessage(c.goodParams))
			if err != nil {
				t.Fatalf("happy path should not error: %v", err)
			}
			b, _ := json.Marshal(got)
			var out map[string]any
			_ = json.Unmarshal(b, &out)
			if out["ok"] != true {
				t.Errorf("ok=%v in %s", out["ok"], b)
			}
		})

		t.Run(c.name+"/empty-params", func(t *testing.T) {
			_, err := c.handler(context.Background(), nil)
			requireAgentErrorCode(t, err, agentwire.CodeInvalidArgument)
		})
		t.Run(c.name+"/missing-id", func(t *testing.T) {
			_, err := c.handler(context.Background(), json.RawMessage(`{"email":"alice@example.com"}`))
			requireAgentErrorCode(t, err, agentwire.CodeInvalidArgument)
		})
		t.Run(c.name+"/bad-email", func(t *testing.T) {
			_, err := c.handler(context.Background(), json.RawMessage(`{"id":"01J","email":"not an email"}`))
			requireAgentErrorCode(t, err, agentwire.CodeInvalidArgument)
		})
	}
}

// TestMailbox_SetQuotaResponse_EchoesValue pins the quota-echo contract
// the reconciler relies on for logging the applied limit.
func TestMailbox_SetQuotaResponse_EchoesValue(t *testing.T) {
	got, err := mailboxSetQuotaHandler(context.Background(),
		json.RawMessage(`{"id":"01J","email":"a@b.com","quota_bytes":9876543210}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp, ok := got.(mailboxSetQuotaResponse)
	if !ok {
		t.Fatalf("type: got %T, want mailboxSetQuotaResponse", got)
	}
	if resp.QuotaBytes != 9876543210 {
		t.Errorf("echo: got %d, want 9876543210", resp.QuotaBytes)
	}
	if !resp.Ok {
		t.Error("ok: got false, want true")
	}
}

// --- mailbox.delete (calls JMAP) -------------------------------------

func TestMailboxDelete_AccountFoundThenDestroyed(t *testing.T) {
	// Two JMAP calls: Account/query -> id, then Account/set destroy.
	srv := newJMAPServer(t, map[string]jmapHandler{
		"x:Account/query": jmapHandlerReturning(jmapQueryResult{
			IDs: []string{"acct-123"}, Total: 1,
		}),
		"x:Account/set": jmapHandlerReturning(jmapSetResult{Destroyed: []string{"acct-123"}}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := mailboxDeleteHandler(context.Background(),
		json.RawMessage(`{"id":"01J","email":"alice@example.com"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	b, _ := json.Marshal(got)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out["ok"] != true {
		t.Errorf("ok=%v", out["ok"])
	}
}

func TestMailboxDelete_NeverSynced_AcksWithoutDestroyCall(t *testing.T) {
	// Account/query returns empty IDs. The destroy route must NOT be
	// called — if it is, the route map below would respond with 400
	// (no such route) and the test fails.
	srv := newJMAPServer(t, map[string]jmapHandler{
		"x:Account/query": jmapHandlerReturning(jmapQueryResult{IDs: nil, Total: 0}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := mailboxDeleteHandler(context.Background(),
		json.RawMessage(`{"id":"01J","email":"ghost@example.com"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	b, _ := json.Marshal(got)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out["ok"] != true {
		t.Errorf("ok=%v", out["ok"])
	}
}

func TestMailboxDelete_BadParams(t *testing.T) {
	_, err := mailboxDeleteHandler(context.Background(), json.RawMessage(`{}`))
	requireAgentErrorCode(t, err, agentwire.CodeInvalidArgument)
}

// --- mailbox.usage ---------------------------------------------------

func TestMailboxUsage_Happy(t *testing.T) {
	srv := newJMAPServer(t, map[string]jmapHandler{
		"x:Account/query": jmapHandlerReturning(jmapQueryResult{
			IDs: []string{"acct-alice"}, Total: 1,
		}),
		"x:Account/get": func(_ json.RawMessage) (any, *jmapFakeError) {
			return jmapGetResult{
				List: []json.RawMessage{
					json.RawMessage(`{"usedDiskQuota":15728640}`),
				},
			}, nil
		},
	})
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := mailboxUsageHandler(context.Background(),
		json.RawMessage(`{"id":"01J","email":"alice@example.com"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp, ok := got.(mailboxUsageResponse)
	if !ok {
		t.Fatalf("type: got %T, want mailboxUsageResponse", got)
	}
	if resp.UsedBytes != 15728640 {
		t.Errorf("UsedBytes: got %d", resp.UsedBytes)
	}
	// MessageCount + LastUsedAt are pinned at zero/empty in v0.16 —
	// see mailbox_jmap.go's accountQuotaView schema-gap comment.
	if resp.MessageCount != 0 {
		t.Errorf("MessageCount: expected 0 (v0.16 gap), got %d", resp.MessageCount)
	}
	if resp.LastUsedAt != "" {
		t.Errorf("LastUsedAt: expected empty (v0.16 gap), got %q", resp.LastUsedAt)
	}
}

func TestMailboxUsage_NeverSynced_ReturnsZeros(t *testing.T) {
	// Never-authed mailbox: Account/query returns no ids, handler
	// skips Account/get entirely and returns zero-value response.
	srv := newJMAPServer(t, map[string]jmapHandler{
		"x:Account/query": jmapHandlerReturning(jmapQueryResult{IDs: nil, Total: 0}),
	})
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := mailboxUsageHandler(context.Background(),
		json.RawMessage(`{"id":"01J","email":"fresh@example.com"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := got.(mailboxUsageResponse)
	if resp.UsedBytes != 0 || resp.MessageCount != 0 {
		t.Errorf("zero expected, got %+v", resp)
	}
	if resp.LastUsedAt != "" {
		t.Errorf("LastUsedAt should be empty for never-synced, got %q", resp.LastUsedAt)
	}
}

func TestMailboxUsage_BadParams(t *testing.T) {
	_, err := mailboxUsageHandler(context.Background(), json.RawMessage(`{}`))
	requireAgentErrorCode(t, err, agentwire.CodeInvalidArgument)
}
