package diagnostic

import (
	"strings"
	"testing"
)

func TestRedact_StripsPasswords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "kv password",
			in:   `2026-04-25 starting with password=hunter2 ok`,
			want: `2026-04-25 starting with password=REDACTED ok`,
		},
		{
			name: "double-dash password",
			in:   `mysqld --user=mysql --password=p4ss w0rd --port=3306`,
			want: `mysqld --user=mysql --password=REDACTED w0rd --port=3306`,
		},
		{
			name: "DSN mysql",
			in:   `connecting to mysql://jabali:supersecret@localhost/jabali_panel`,
			want: `connecting to mysql://jabali:REDACTED@localhost/jabali_panel`,
		},
		{
			name: "DSN redis",
			in:   `redis://app:secret123@127.0.0.1:6379/0`,
			want: `redis://app:REDACTED@127.0.0.1:6379/0`,
		},
		{
			name: "Cookie header",
			in:   `GET /api Cookie: ory_kratos_session=abcdef.1234`,
			want: `GET /api Cookie: REDACTED`,
		},
		{
			name: "Bearer token",
			in:   `Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.AbC`,
			want: `Authorization=REDACTED REDACTED`,
			// Bearer pass strips the token first; then the generic
			// Authorization k:v pass strips up to the next whitespace,
			// leaving the trailing literal "REDACTED" word in the line.
			// What matters: the original token never makes it through.
		},
		{
			name: "API key colon",
			in:   `api_key: sk-abc123XYZ`,
			want: `api_key=REDACTED`,
		},
		{
			name: "secret colon",
			in:   `db_secret: ssss`,
			want: `db_secret=REDACTED`,
		},
		{
			name: "no secrets",
			in:   `everything fine here, no creds at all`,
			want: `everything fine here, no creds at all`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _ := Redact([]byte(c.in))
			if got := string(out); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRedact_PreservesContext(t *testing.T) {
	in := "2026-04-25T10:00:00Z INFO jabali-panel password=hunter2 user_id=01ABC done"
	out, n := Redact([]byte(in))
	got := string(out)
	if n == 0 {
		t.Fatalf("expected at least one redaction")
	}
	if !strings.Contains(got, "2026-04-25T10:00:00Z") || !strings.Contains(got, "user_id=01ABC") {
		t.Errorf("context lost: %q", got)
	}
}

func TestRedact_KratosSession(t *testing.T) {
	in := "Set-Cookie: ory_kratos_session=eyJ0eXAiOiJKV1Qi.abc; Path=/"
	out, n := Redact([]byte(in))
	if n == 0 {
		t.Fatalf("expected redaction")
	}
	if strings.Contains(string(out), "eyJ0eXAi") {
		t.Errorf("session token leaked: %s", string(out))
	}
}

func TestRedact_CountsRedactions(t *testing.T) {
	in := `password=a token=b api_key=c`
	_, n := Redact([]byte(in))
	if n < 3 {
		t.Errorf("want >=3 redactions, got %d", n)
	}
}
