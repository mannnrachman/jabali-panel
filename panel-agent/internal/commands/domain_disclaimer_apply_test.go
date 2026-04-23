package commands

import (
	"strings"
	"testing"
)

func TestRenderDisclaimerSieve_Requires(t *testing.T) {
	got := renderDisclaimerSieve("example.com", "Confidential.")
	want := `require ["envelope","variables","mime","foreverypart","extracttext","replace"];`
	if !strings.Contains(got, want) {
		t.Fatalf("render missing required extensions line.\nfirst line: %q\nwant: %q", firstLine(got), want)
	}
}

func TestRenderDisclaimerSieve_EnvelopeGuard(t *testing.T) {
	got := renderDisclaimerSieve("example.com", "hi")
	if !strings.Contains(got, `if envelope :domain "from" "example.com" {`) {
		t.Fatalf("missing envelope :domain guard.\n%s", got)
	}
}

func TestRenderDisclaimerSieve_BothBranches(t *testing.T) {
	got := renderDisclaimerSieve("example.com", "hi")
	if !strings.Contains(got, `if header :mime :contenttype :is "Content-Type" "text/plain"`) {
		t.Fatalf("missing text/plain branch.\n%s", got)
	}
	if !strings.Contains(got, `elsif header :mime :contenttype :is "Content-Type" "text/html"`) {
		t.Fatalf("missing text/html branch.\n%s", got)
	}
}

func TestRenderDisclaimerSieve_ExtracttextReplace(t *testing.T) {
	got := renderDisclaimerSieve("example.com", "hi")
	// Must extract body before replace, both branches.
	if strings.Count(got, `extracttext "jabali_orig"`) != 2 {
		t.Fatalf("expected 2 extracttext calls (plain + html).\n%s", got)
	}
	if !strings.Contains(got, `replace "${jabali_orig}\n\n-- \nhi\n"`) {
		t.Fatalf("text/plain replace line malformed.\n%s", got)
	}
	if !strings.Contains(got, `replace "${jabali_orig}<hr><p>hi</p>"`) {
		t.Fatalf("text/html replace line malformed.\n%s", got)
	}
}

func TestSieveEscape(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`plain`, `plain`},
		{`a"b`, `a\"b`},
		{`a\b`, `a\\b`},
		{"line1\nline2", `line1\nline2`},
		{"mix \"both\"\nplus\\back", `mix \"both\"\nplus\\back`},
	}
	for _, c := range cases {
		if got := sieveEscape(c.in); got != c.want {
			t.Errorf("sieveEscape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHTMLEscape(t *testing.T) {
	got := htmlEscape(`<img src="x" onerror=alert(1)>`)
	want := `&lt;img src=&quot;x&quot; onerror=alert(1)&gt;`
	if got != want {
		t.Fatalf("htmlEscape = %q, want %q", got, want)
	}
}

func TestRenderDisclaimerSieve_InjectionResistance(t *testing.T) {
	// Operator text must not be able to inject HTML tags into the html
	// branch. Sieve-string escapes are covered by TestSieveEscape.
	got := renderDisclaimerSieve("example.com", `"; stop; "<script>alert(1)</script>`)
	// Isolate the html branch (from `elsif` onward).
	elsifIdx := strings.Index(got, "elsif")
	if elsifIdx < 0 {
		t.Fatalf("render missing elsif html branch.\n%s", got)
	}
	htmlBranch := got[elsifIdx:]
	// HTML branch must have `<script>` escaped — it must not appear literally.
	if strings.Contains(htmlBranch, `<script>`) {
		t.Fatalf("unescaped <script> reached html replace.\n%s", htmlBranch)
	}
	if !strings.Contains(htmlBranch, `&lt;script&gt;alert(1)&lt;/script&gt;`) {
		t.Fatalf("expected HTML-escaped script in html branch.\n%s", htmlBranch)
	}
	// Sieve branch must have the quote escaped (presence of \" preceding
	// the injected semicolons) in BOTH branches.
	if !strings.Contains(got, `\"; stop; \"`) {
		t.Fatalf("expected sieve-escaped quotes around injected text.\n%s", got)
	}
}

func TestSanitizeScriptName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"example.com", "example-com"},
		{"MyDomain.Com", "mydomain-com"},
		{"a_b_c", "a-b-c"},
		{"sub.domain.co.uk", "sub-domain-co-uk"},
	}
	for _, c := range cases {
		if got := sanitizeScriptName(c.in); got != c.want {
			t.Errorf("sanitizeScriptName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
