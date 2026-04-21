package dkim

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestGenerateEd25519_KeyShape(t *testing.T) {
	t.Parallel()

	k, err := GenerateEd25519()
	if err != nil {
		t.Fatalf("GenerateEd25519: %v", err)
	}

	// Private form: base64 StdEncoding of a 32-byte seed is exactly 44
	// chars (32 bytes × 4/3 rounded up + padding).
	if got, want := len(k.PrivateRawBase64), 44; got != want {
		t.Errorf("PrivateRawBase64 len: got %d, want %d", got, want)
	}
	seed, err := base64.StdEncoding.DecodeString(string(k.PrivateRawBase64))
	if err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	if len(seed) != ed25519.SeedSize {
		t.Errorf("seed size: got %d, want %d", len(seed), ed25519.SeedSize)
	}

	// Public DKIM TXT: must start with the canonical tag set.
	if !bytes.HasPrefix(k.PublicDKIMTxt, []byte("v=DKIM1; k=ed25519; p=")) {
		t.Fatalf("TXT prefix wrong: %q", k.PublicDKIMTxt)
	}
	// No trailing newline, no surrounding quotes.
	if bytes.HasSuffix(k.PublicDKIMTxt, []byte("\n")) {
		t.Error("TXT has trailing newline")
	}
	if bytes.ContainsAny(k.PublicDKIMTxt, `"'`) {
		t.Error("TXT has surrounding quotes")
	}

	// Extract the p= value and verify it round-trips to a valid 32-byte
	// public key that matches the private seed's derived public key.
	after := bytes.TrimPrefix(k.PublicDKIMTxt, []byte("v=DKIM1; k=ed25519; p="))
	pub, err := base64.StdEncoding.DecodeString(string(after))
	if err != nil {
		t.Fatalf("decode pub: %v", err)
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Errorf("pub size: got %d, want %d", len(pub), ed25519.PublicKeySize)
	}

	// Seed-derived private must match the DKIM-record public.
	derived := ed25519.NewKeyFromSeed(seed)
	derivedPub, ok := derived.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("derived.Public() is %T, want ed25519.PublicKey", derived.Public())
	}
	if !bytes.Equal(pub, derivedPub) {
		t.Errorf("seed-derived public does not match DKIM TXT public")
	}
}

func TestGenerateEd25519_Uniqueness(t *testing.T) {
	t.Parallel()

	// 10 generations should all be distinct. Ed25519 is 2^256 space so
	// collisions here would mean crypto/rand is broken.
	seen := make(map[string]bool, 10)
	for i := 0; i < 10; i++ {
		k, err := GenerateEd25519()
		if err != nil {
			t.Fatalf("GenerateEd25519 iter %d: %v", i, err)
		}
		s := string(k.PrivateRawBase64)
		if seen[s] {
			t.Fatalf("collision on iter %d: %s", i, s)
		}
		seen[s] = true
	}
}

func TestWritePrivate_AtomicAndMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "example.com.key")

	k, err := GenerateEd25519()
	if err != nil {
		t.Fatalf("GenerateEd25519: %v", err)
	}
	if err := WritePrivate(target, k.PrivateRawBase64); err != nil {
		t.Fatalf("WritePrivate: %v", err)
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := fi.Mode() & fs.ModePerm; mode != 0o600 {
		t.Errorf("mode: got %o, want 0600", mode)
	}

	// Content: base64 seed followed by a single newline.
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := append(append([]byte{}, k.PrivateRawBase64...), '\n')
	if !bytes.Equal(got, want) {
		t.Errorf("content mismatch:\n got %q\nwant %q", got, want)
	}

	// No leftover temp files in the dir.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".dkim-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestWritePrivate_Overwrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "key")

	if err := WritePrivate(target, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WritePrivate(target, []byte("second")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "second\n" {
		t.Errorf("overwrite: got %q, want %q", got, "second\n")
	}
}

func TestWritePrivate_Empty(t *testing.T) {
	t.Parallel()
	err := WritePrivate(filepath.Join(t.TempDir(), "k"), nil)
	if err == nil {
		t.Error("empty input: expected error, got nil")
	}
}

func TestWritePrivate_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	// Fire 8 concurrent writes to the same target. The atomic rename
	// contract means each call produces a valid file; no torn state.
	dir := t.TempDir()
	target := filepath.Join(dir, "key")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k, err := GenerateEd25519()
			if err != nil {
				t.Errorf("gen %d: %v", i, err)
				return
			}
			if err := WritePrivate(target, k.PrivateRawBase64); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// File exists, has mode 0600, and content parses.
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := fi.Mode() & fs.ModePerm; mode != 0o600 {
		t.Errorf("final mode: got %o, want 0600", mode)
	}
	if _, err := LoadEd25519(target); err != nil {
		t.Errorf("final content unparsable: %v", err)
	}
}

func TestLoadEd25519_RoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "k")

	k, err := GenerateEd25519()
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if err := WritePrivate(target, k.PrivateRawBase64); err != nil {
		t.Fatalf("write: %v", err)
	}
	seed, err := LoadEd25519(target)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(seed) != ed25519.SeedSize {
		t.Errorf("seed len: got %d, want %d", len(seed), ed25519.SeedSize)
	}

	// Seed must reproduce the same public key the DKIM TXT advertises.
	after := bytes.TrimPrefix(k.PublicDKIMTxt, []byte("v=DKIM1; k=ed25519; p="))
	wantPub, _ := base64.StdEncoding.DecodeString(string(after))
	gotPub, _ := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !bytes.Equal(gotPub, wantPub) {
		t.Error("loaded seed does not reproduce DKIM public key")
	}
}

func TestLoadEd25519_InvalidContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
	}{
		{"not-base64", "###not-base64###"},
		{"wrong-length", base64.StdEncoding.EncodeToString([]byte("too short"))},
		{"empty", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(dir, tc.name)
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatalf("prep: %v", err)
			}
			_, err := LoadEd25519(p)
			if !errors.Is(err, ErrInvalidSeed) {
				t.Errorf("err: got %v, want Is(ErrInvalidSeed)", err)
			}
		})
	}
}

func TestSeedToPKCS8PEM(t *testing.T) {
	t.Parallel()

	// Round-trip: generate a seed, wrap as PKCS#8 PEM, then parse it back
	// with x509.ParsePKCS8PrivateKey and verify the seed matches.
	k, err := GenerateEd25519()
	if err != nil {
		t.Fatalf("GenerateEd25519: %v", err)
	}
	seed, err := base64.StdEncoding.DecodeString(string(k.PrivateRawBase64))
	if err != nil {
		t.Fatalf("decode seed: %v", err)
	}

	p, err := SeedToPKCS8PEM(seed)
	if err != nil {
		t.Fatalf("SeedToPKCS8PEM: %v", err)
	}
	if !bytes.HasPrefix(p, []byte("-----BEGIN PRIVATE KEY-----\n")) {
		t.Errorf("PEM missing expected BEGIN header: %q", p[:64])
	}
	if !bytes.HasSuffix(p, []byte("-----END PRIVATE KEY-----\n")) {
		t.Errorf("PEM missing expected END footer")
	}

	// Parse the PEM back — it should decode into an Ed25519 key with the
	// same seed. This is exactly what Stalwart v0.16's
	// `Ed25519Key::from_pkcs8_maybe_unchecked_der` does after PEM-decoding.
	block, _ := pem.Decode(p)
	if block == nil {
		t.Fatal("pem.Decode returned nil block")
	}
	if block.Type != "PRIVATE KEY" {
		t.Errorf("PEM type: got %q, want PRIVATE KEY", block.Type)
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse PKCS#8: %v", err)
	}
	priv, ok := parsedKey.(ed25519.PrivateKey)
	if !ok {
		t.Fatalf("parsed key type: got %T, want ed25519.PrivateKey", parsedKey)
	}
	if !bytes.Equal(priv.Seed(), seed) {
		t.Error("round-trip seed mismatch")
	}
}

func TestSeedToPKCS8PEM_InvalidSeed(t *testing.T) {
	t.Parallel()
	// 31 bytes — one short. Any length other than 32 should error.
	_, err := SeedToPKCS8PEM(make([]byte, 31))
	if !errors.Is(err, ErrInvalidSeed) {
		t.Errorf("err: got %v, want Is(ErrInvalidSeed)", err)
	}
}

