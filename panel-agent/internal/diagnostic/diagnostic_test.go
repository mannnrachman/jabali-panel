package diagnostic

import (
	"archive/tar"
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
)

// TestEncrypt_RoundTrips proves the bundle can be decrypted by the
// matching identity and that redaction has stripped the seeded password.
func TestEncrypt_RoundTrips(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	pubKey := id.Recipient().String()

	files := []collectedFile{
		{Name: "00-info.txt", Body: []byte("running mysql password=hunter2 in production")},
		{Name: "svc/jabali-panel.journal.txt", Body: []byte("Cookie: ory_kratos_session=ABC.def\nstartup ok")},
	}
	report, err := Encrypt(files, pubKey)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if report.RedactionCount < 2 {
		t.Errorf("expected >=2 redactions, got %d", report.RedactionCount)
	}
	if report.FileCount != 2 {
		t.Errorf("FileCount=%d want 2", report.FileCount)
	}
	if report.CiphertextB64 == "" {
		t.Fatalf("empty ciphertext")
	}

	// Decrypt with the matching identity, untar, verify content.
	ciph, err := base64.StdEncoding.DecodeString(report.CiphertextB64)
	if err != nil {
		t.Fatalf("base64: %v", err)
	}
	dec, err := age.Decrypt(bytes.NewReader(ciph), id)
	if err != nil {
		t.Fatalf("age decrypt: %v", err)
	}
	tarBytes, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("read tar: %v", err)
	}
	tr := tar.NewReader(bytes.NewReader(tarBytes))
	seen := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		body, _ := io.ReadAll(tr)
		seen[hdr.Name] = string(body)
	}
	if !strings.Contains(seen["00-info.txt"], "REDACTED") {
		t.Errorf("info file not redacted: %q", seen["00-info.txt"])
	}
	if strings.Contains(seen["00-info.txt"], "hunter2") {
		t.Errorf("password leaked: %q", seen["00-info.txt"])
	}
	journal := seen["svc/jabali-panel.journal.txt"]
	if strings.Contains(journal, "ABC.def") {
		t.Errorf("session cookie leaked: %q", journal)
	}
}

func TestEncrypt_BadRecipient(t *testing.T) {
	_, err := Encrypt(nil, "not-an-age-recipient")
	if err == nil {
		t.Fatal("expected error on malformed recipient")
	}
}
