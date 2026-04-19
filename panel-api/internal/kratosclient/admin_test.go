package kratosclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAdminClient(server *httptest.Server) *Client {
	// Expose both URLs on the same test server — the fake routes for public
	// (/self-service/*) and admin (/admin/*) coexist on the same mux in tests.
	return &Client{
		publicURL:  strings.TrimSuffix(server.URL, "/"),
		adminURL:   strings.TrimSuffix(server.URL, "/"),
		httpClient: server.Client(),
	}
}

func TestCreateIdentityWithPassword_SendsCorrectPayload(t *testing.T) {
	t.Parallel()
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/admin/identities" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "wrong endpoint", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"abc-123","traits":{"email":"u@example.com","is_admin":false}}`))
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	id, err := c.CreateIdentityWithPassword(context.Background(),
		AdminTraits{Email: "u@example.com", Username: "u", IsAdmin: false},
		"$2a$12$F1rL1qVOFx9r1z4L5QhqQOqzxN3V5K9X2Y8Z1A2B3C4D5E6F7G8H9I",
	)
	if err != nil {
		t.Fatalf("CreateIdentityWithPassword: %v", err)
	}
	if id != "abc-123" {
		t.Fatalf("id = %q, want abc-123", id)
	}
	if captured["schema_id"] != "default" {
		t.Errorf("schema_id = %v, want default", captured["schema_id"])
	}
	traits, ok := captured["traits"].(map[string]any)
	if !ok {
		t.Fatalf("traits missing")
	}
	if _, ok := traits["is_admin"]; !ok {
		t.Error("is_admin must be present even when false (no omitempty)")
	}
	creds, ok := captured["credentials"].(map[string]any)
	if !ok {
		t.Fatalf("credentials missing")
	}
	pw := creds["password"].(map[string]any)["config"].(map[string]any)["hashed_password"]
	if _, ok := pw.(string); !ok {
		t.Errorf("hashed_password missing in credentials payload")
	}
}

func TestCreateIdentityWithPassword_IsAdminTrueRoundtrips(t *testing.T) {
	t.Parallel()
	var captured AdminTraits
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Traits AdminTraits `json:"traits"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		captured = body.Traits
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ok"}`))
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	_, err := c.CreateIdentityWithPassword(context.Background(),
		AdminTraits{Email: "admin@x", IsAdmin: true},
		"$2a$12$F1rL1qVOFx9r1z4L5QhqQOqzxN3V5K9X2Y8Z1A2B3C4D5E6F7G8H9I",
	)
	if err != nil {
		t.Fatalf("CreateIdentityWithPassword: %v", err)
	}
	if !captured.IsAdmin {
		t.Error("IsAdmin=true was lost in the wire payload")
	}
}

func TestCreateIdentityWithPassword_RejectsShortHash(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server must not be hit — local validation should reject before the request")
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	_, err := c.CreateIdentityWithPassword(context.Background(),
		AdminTraits{Email: "u@x"}, "too-short",
	)
	if err == nil {
		t.Fatal("expected error on short bcrypt hash")
	}
}

func TestCreateIdentityWithPassword_EmptyIDIsError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":""}`))
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	_, err := c.CreateIdentityWithPassword(context.Background(),
		AdminTraits{Email: "u@x"},
		"$2a$12$F1rL1qVOFx9r1z4L5QhqQOqzxN3V5K9X2Y8Z1A2B3C4D5E6F7G8H9I",
	)
	if err == nil {
		t.Fatal("expected error on empty id")
	}
}

func TestDeleteIdentity_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/admin/identities/abc-123" {
			t.Errorf("wrong path: %s %s", r.Method, r.URL.Path)
			http.Error(w, "", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	if err := c.DeleteIdentity(context.Background(), "abc-123"); err != nil {
		t.Fatalf("DeleteIdentity: %v", err)
	}
}

func TestDeleteIdentity_404IsIdempotent(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	if err := c.DeleteIdentity(context.Background(), "gone"); err != nil {
		t.Fatalf("404 must be idempotent: %v", err)
	}
}

func TestDeleteIdentity_EmptyIDShortCircuits(t *testing.T) {
	t.Parallel()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	if err := c.DeleteIdentity(context.Background(), ""); err != nil {
		t.Fatalf("empty id should not error: %v", err)
	}
	if hits != 0 {
		t.Errorf("empty id triggered %d HTTP calls (want 0)", hits)
	}
}

func TestDeleteIdentity_ServerErrorPropagates(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	err := c.DeleteIdentity(context.Background(), "id")
	if err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestListIdentitiesPage_NoLinkHeader(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"a","traits":{"email":"A@x","is_admin":false}}]`))
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	ids, next, err := c.ListIdentitiesPage(context.Background(), 10, "")
	if err != nil {
		t.Fatalf("ListIdentitiesPage: %v", err)
	}
	if len(ids) != 1 || ids[0].ID != "a" {
		t.Fatalf("unexpected page contents: %+v", ids)
	}
	if next != "" {
		t.Errorf("no Link header → next token must be empty, got %q", next)
	}
}

func TestAllIdentitiesByEmail_FollowsPagination(t *testing.T) {
	t.Parallel()
	// Two-page response: first page advertises next=PAGE2, second page has no Link.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("page_token")
		w.Header().Set("Content-Type", "application/json")
		switch token {
		case "":
			w.Header().Set("Link", `<http://x/admin/identities?per_page=250&page_token=PAGE2>; rel="next"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"1","traits":{"email":"First@Example.COM","is_admin":false}}]`))
		case "PAGE2":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":"2","traits":{"email":"second@example.com","is_admin":true}}]`))
		}
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	byEmail, err := c.AllIdentitiesByEmail(context.Background())
	if err != nil {
		t.Fatalf("AllIdentitiesByEmail: %v", err)
	}
	if got := byEmail["first@example.com"]; got != "1" {
		t.Errorf("email lowercased+mapped wrong: got %q", got)
	}
	if got := byEmail["second@example.com"]; got != "2" {
		t.Errorf("second page missing: got %q", got)
	}
}

func TestParseNextPageToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"empty", "", ""},
		{"no rel=next", `<http://x/p?page_token=A>; rel="first"`, ""},
		{"next present", `<http://x/p?per_page=5&page_token=ABC>; rel="next"`, "ABC"},
		{"multiple rels", `<http://x?page_token=A>; rel="first", <http://x?page_token=B>; rel="next"`, "B"},
		{"rel=next without page_token", `<http://x/p?per_page=5>; rel="next"`, ""},
	}
	for _, tc := range cases {
		if got := parseNextPageToken(tc.header); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestVerifyBcryptPassthrough_HappyPath(t *testing.T) {
	t.Parallel()
	var createdID string
	var loginFlowID = "flow-xyz"
	var deleteCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/admin/identities":
			createdID = "canary-" + strings.TrimPrefix(r.URL.Path, "/admin/identities/")
			createdID = "kratos-id-42"
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"kratos-id-42","traits":{"email":"c@j.invalid","is_admin":false}}`))

		case r.Method == http.MethodGet && r.URL.Path == "/self-service/login/api":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"` + loginFlowID + `"}`))

		case r.Method == http.MethodPost && r.URL.Path == "/self-service/login":
			if got := r.URL.Query().Get("flow"); got != loginFlowID {
				t.Errorf("login POST flow id = %q, want %q", got, loginFlowID)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"session_token":"sess-abc","session":{"id":"s"}}`))

		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/identities/"):
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	if err := c.VerifyBcryptPassthrough(context.Background()); err != nil {
		t.Fatalf("VerifyBcryptPassthrough: %v", err)
	}
	if createdID == "" {
		t.Error("create was not called")
	}
	if !deleteCalled {
		t.Error("canary was not deleted")
	}
}

func TestVerifyBcryptPassthrough_LoginFailSurfacesError(t *testing.T) {
	t.Parallel()
	deleteCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/admin/identities":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"canary-1","traits":{"email":"c@j.invalid"}}`))
		case r.URL.Path == "/self-service/login/api":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"flow"}`))
		case r.URL.Path == "/self-service/login":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_credentials"}`))
		case strings.HasPrefix(r.URL.Path, "/admin/identities/"):
			deleteCalled = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	c := newAdminClient(srv)
	err := c.VerifyBcryptPassthrough(context.Background())
	if err == nil {
		t.Fatal("expected error when login fails")
	}
	if !strings.Contains(err.Error(), "login failed") {
		t.Errorf("error must mention login failure for operator clarity: %v", err)
	}
	if !deleteCalled {
		t.Error("canary identity must be deleted even when login fails")
	}
}
