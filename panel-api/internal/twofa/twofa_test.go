package twofa

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestNewEnrolment_ReturnsUsableSecret(t *testing.T) {
	t.Parallel()

	e, err := NewEnrolment("alice@example.com")
	if err != nil {
		t.Fatalf("NewEnrolment: %v", err)
	}
	if e.Secret == "" {
		t.Fatal("empty secret")
	}
	if !strings.HasPrefix(e.OtpauthURL, "otpauth://totp/") {
		t.Fatalf("OtpauthURL missing otpauth scheme: %q", e.OtpauthURL)
	}
	if !strings.Contains(e.OtpauthURL, "alice@example.com") {
		t.Fatalf("OtpauthURL missing account email: %q", e.OtpauthURL)
	}
	if !strings.Contains(e.OtpauthURL, "Jabali") {
		t.Fatalf("OtpauthURL missing issuer: %q", e.OtpauthURL)
	}
	if !ValidBase32Secret(e.Secret) {
		t.Fatalf("secret is not valid base32: %q", e.Secret)
	}
}

func TestVerify_MatchesFreshTOTP(t *testing.T) {
	t.Parallel()

	e, err := NewEnrolment("alice@example.com")
	if err != nil {
		t.Fatalf("NewEnrolment: %v", err)
	}

	// Compute the current window's code using the same library, then
	// feed it back through Verify. Avoids wall-clock flakiness.
	code, err := totp.GenerateCode(e.Secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !Verify(e.Secret, code) {
		t.Fatalf("Verify rejected a freshly-generated code")
	}
}

func TestVerify_RejectsBadCode(t *testing.T) {
	t.Parallel()

	e, _ := NewEnrolment("bob@example.com")
	if Verify(e.Secret, "000000") {
		t.Fatal("Verify accepted all-zeros code (astronomically unlikely match)")
	}
	if Verify(e.Secret, "") {
		t.Fatal("Verify accepted empty code")
	}
	if Verify("", "123456") {
		t.Fatal("Verify accepted empty secret")
	}
}

func TestNewBackupCodes_Shape(t *testing.T) {
	t.Parallel()

	codes, err := NewBackupCodes()
	if err != nil {
		t.Fatalf("NewBackupCodes: %v", err)
	}
	if got, want := len(codes), BackupCodeCount; got != want {
		t.Fatalf("got %d codes, want %d", got, want)
	}
	seen := make(map[string]struct{}, len(codes))
	for i, c := range codes {
		if len(c) != BackupCodeDigits {
			t.Errorf("code[%d]=%q has len %d, want %d", i, c, len(c), BackupCodeDigits)
		}
		for _, ch := range c {
			if ch < '0' || ch > '9' {
				t.Errorf("code[%d]=%q contains non-digit %q", i, c, ch)
			}
		}
		if _, dup := seen[c]; dup {
			t.Errorf("code[%d]=%q duplicated", i, c)
		}
		seen[c] = struct{}{}
	}
}

func TestHashCode_MatchCode_Roundtrip(t *testing.T) {
	t.Parallel()

	const code = "12345678"
	h, err := HashCode(code)
	if err != nil {
		t.Fatalf("HashCode: %v", err)
	}
	if h == code {
		t.Fatal("HashCode returned plaintext")
	}
	if !MatchCode(h, code) {
		t.Fatal("MatchCode rejected the correct code")
	}
	if MatchCode(h, "87654321") {
		t.Fatal("MatchCode accepted the wrong code")
	}
	if MatchCode("", code) {
		t.Fatal("MatchCode accepted empty hash")
	}
	if MatchCode(h, "") {
		t.Fatal("MatchCode accepted empty code")
	}
}

func TestValidBase32Secret(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid", "JBSWY3DPEHPK3PXP", true},
		{"empty", "", false},
		{"lowercase (not valid base32 in std encoding)", "jbswy3dpehpk3pxp", false},
		{"padding-forbidden form accepts unpadded", "JBSWY3DPEHPK3PXP", true},
		{"garbage", "not-base32!!!", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidBase32Secret(tc.in); got != tc.want {
				t.Errorf("ValidBase32Secret(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestConstEq(t *testing.T) {
	t.Parallel()

	if !ConstEq("abc", "abc") {
		t.Fatal("ConstEq returned false for equal strings")
	}
	if ConstEq("abc", "abd") {
		t.Fatal("ConstEq returned true for different strings")
	}
	if ConstEq("abc", "abcd") {
		t.Fatal("ConstEq returned true for different-length strings")
	}
	if !ConstEq("", "") {
		t.Fatal("ConstEq returned false for empty strings")
	}
}
