package commands

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// fakeOKServer replies 200 to every request. Shared by the four cache-
// invalidate command happy-paths.
func fakeOKServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

// Parity test: every one of the four cache-invalidate commands must
// behave identically on happy and error paths. Covering all four here
// instead of replicating the matrix in four separate files keeps the
// noise down without weakening the wire guarantee — the contract test
// (panel-api/internal/agent/mailbox_contract_test.go) still pins the
// panel-agent JSON shape for each one independently.
func TestMailbox_CacheInvalidate_HappyAndError(t *testing.T) {
	type cmd struct {
		name    string
		handler func(context.Context, json.RawMessage) (any, error)
		// Request body minimum fields (id + email) — set_quota adds quota_bytes,
		// but we keep the extra field only where needed to catch parse errors.
		goodParams string
	}
	cmds := []cmd{
		{"mailbox.create", mailboxCreateHandler, `{"id":"01J","email":"alice@example.com"}`},
		{"mailbox.delete", mailboxDeleteHandler, `{"id":"01J","email":"alice@example.com"}`},
		{"mailbox.set_quota", mailboxSetQuotaHandler, `{"id":"01J","email":"alice@example.com","quota_bytes":1024}`},
		{"mailbox.set_password", mailboxSetPasswordHandler, `{"id":"01J","email":"alice@example.com"}`},
	}

	for _, c := range cmds {
		c := c
		t.Run(c.name+"/happy", func(t *testing.T) {
			srv := fakeOKServer()
			defer srv.Close()
			wireJMAP(t, srv)

			got, err := c.handler(context.Background(), json.RawMessage(c.goodParams))
			if err != nil {
				t.Fatalf("%s happy: %v", c.name, err)
			}
			// All four commands return a struct whose "ok" field marshals true.
			b, _ := json.Marshal(got)
			var out map[string]any
			_ = json.Unmarshal(b, &out)
			if out["ok"] != true {
				t.Errorf("%s: ok=%v in %s", c.name, out["ok"], b)
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

		t.Run(c.name+"/stalwart-500", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()
			wireJMAP(t, srv)

			_, err := c.handler(context.Background(), json.RawMessage(c.goodParams))
			requireAgentErrorCode(t, err, agentwire.CodeInternal)
		})
	}
}

// TestMailbox_SetQuotaResponse_EchoesValue pins the quota-echo contract
// the reconciler/handler rely on for logging the applied limit.
func TestMailbox_SetQuotaResponse_EchoesValue(t *testing.T) {
	srv := fakeOKServer()
	defer srv.Close()
	wireJMAP(t, srv)

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

// --- mailbox.usage --------------------------------------------------

func TestMailboxUsage_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"usedBytes":15728640,"messageCount":42,"lastUsedAt":"2026-04-21T19:03:00Z"}`))
	}))
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
		t.Errorf("UsedBytes: got %d, want 15728640", resp.UsedBytes)
	}
	if resp.MessageCount != 42 {
		t.Errorf("MessageCount: got %d, want 42", resp.MessageCount)
	}
	if resp.LastUsedAt != "2026-04-21T19:03:00Z" {
		t.Errorf("LastUsedAt: got %q, want 2026-04-21T19:03:00Z", resp.LastUsedAt)
	}
}

func TestMailboxUsage_NeverUsed_ReturnsZeros(t *testing.T) {
	// Stalwart 404 means "no principal info cached / never auth'd" —
	// the panel expects zeros, not an error, because the row exists
	// in SQL and hasn't been touched yet. The sampler then writes 0
	// bytes into mailboxes.last_usage_bytes, which is the correct UI
	// read ("Alice hasn't used any of her 1 GiB yet").
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	wireJMAP(t, srv)

	got, err := mailboxUsageHandler(context.Background(),
		json.RawMessage(`{"id":"01J","email":"alice@example.com"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	resp := got.(mailboxUsageResponse)
	if resp.UsedBytes != 0 || resp.MessageCount != 0 {
		t.Errorf("zero expected, got %+v", resp)
	}
	if resp.LastUsedAt != "" {
		t.Errorf("LastUsedAt should be empty for never-used, got %q", resp.LastUsedAt)
	}
}

func TestMailboxUsage_BadParams(t *testing.T) {
	_, err := mailboxUsageHandler(context.Background(), json.RawMessage(`{}`))
	requireAgentErrorCode(t, err, agentwire.CodeInvalidArgument)
}

// requireAgentErrorCode asserts err is an *AgentError with the given code.
func requireAgentErrorCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", wantCode)
	}
	var ae *agentwire.AgentError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if ae.Code != wantCode {
		t.Errorf("err code: got %q, want %q (msg: %s)", ae.Code, wantCode, ae.Message)
	}
}
