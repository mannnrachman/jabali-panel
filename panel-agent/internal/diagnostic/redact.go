// Package diagnostic collects host status, redacts secrets, and encrypts
// the result with age. ADR-0064 explains the trust model: ciphertext is
// safe to paste in a public GitHub issue, but only when redaction has
// already stripped credentials BEFORE encryption — a future private-key
// compromise would otherwise turn every report into a credential dump.
package diagnostic

import (
	"regexp"
)

// redactor is one regex + a replacement string. We use $1 etc. to keep the
// surrounding context (timestamps, log level, hostname) so a debugger can
// still trace the lineage of a redacted line.
type redactor struct {
	re   *regexp.Regexp
	repl string
}

// redactors is intentionally a deny-list: known-bad tokens we strip on
// sight. Logs we don't recognise pass through. Tradeoff: an unknown secret
// format leaks; the alternative (allow-list) loses too much debug signal.
//
// ORDER MATTERS. Specific patterns (Bearer XYZ inside an Authorization
// header) must run before generic key=value strippers, otherwise the
// generic pass eats only "Authorization=…" up to the first space and
// leaves the Bearer token bare.
var redactors = []redactor{
	// Specific HTTP auth tokens FIRST.
	{regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._\-+/=]+`), "Bearer REDACTED"},
	{regexp.MustCompile(`ory_kratos_session=[A-Za-z0-9._\-+/=]+`), "ory_kratos_session=REDACTED"},
	{regexp.MustCompile(`(?i)Cookie:\s*\S+`), "Cookie: REDACTED"},

	// DSN style: keep the user but strip the password.
	{regexp.MustCompile(`(mysql|postgres|postgresql|mariadb|redis|amqp)://([^:/@]+):[^@]+@`), "$1://$2:REDACTED@"},

	// Bare key=value forms. Greedy-match value up to the first whitespace
	// or quote so we don't eat surrounding fields.
	{regexp.MustCompile(`(?i)(password)\s*=\s*[^\s'"&]+`), "$1=REDACTED"},
	{regexp.MustCompile(`(?i)--password=[^\s'"&]+`), "--password=REDACTED"},
	{regexp.MustCompile(`(?i)(api[_-]?key)\s*[=:]\s*[^\s'"&]+`), "$1=REDACTED"},
	{regexp.MustCompile(`(?i)(authorization)\s*[=:]\s*[^\s'"&]+`), "$1=REDACTED"},
	{regexp.MustCompile(`(?i)(token)\s*[=:]\s*[^\s'"&]+`), "$1=REDACTED"},
	{regexp.MustCompile(`(?i)(secret)\s*[=:]\s*[^\s'"&]+`), "$1=REDACTED"},
}

// Redact runs every regex over the input and returns (redacted bytes,
// redaction count). Caller can show the count to the operator so they
// know how many secrets were stripped.
func Redact(in []byte) ([]byte, int) {
	out := in
	count := 0
	for _, r := range redactors {
		matches := r.re.FindAll(out, -1)
		count += len(matches)
		out = r.re.ReplaceAll(out, []byte(r.repl))
	}
	return out, count
}
