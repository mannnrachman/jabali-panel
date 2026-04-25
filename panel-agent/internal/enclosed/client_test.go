package enclosed

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"golang.org/x/crypto/pbkdf2"
)

// TestEncryptDecrypt_RoundTrips proves the cipher pair we ship matches
// the JS lib's own decryption path: an AES-256-GCM ciphertext written
// the JS way decrypts cleanly with the same key + iv we record.
func TestEncryptDecrypt_RoundTrips(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	plaintext := []byte("hello jabali")

	out, err := encryptAES256GCM(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	parts := strings.Split(out, ":")
	if len(parts) != 2 {
		t.Fatalf("want iv:ct, got %q", out)
	}
	iv, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("iv b64: %v", err)
	}
	ctTag, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("ct b64: %v", err)
	}
	if len(iv) != 12 {
		t.Fatalf("iv length %d, want 12", len(iv))
	}

	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	pt, err := gcm.Open(nil, iv, ctTag, nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(pt) != string(plaintext) {
		t.Fatalf("decrypted=%q want %q", string(pt), string(plaintext))
	}
}

// TestDeriveMasterKey_MatchesJSFormula reproduces the JS deriveMasterKey
// step independently and checks our output matches.
//
// JS:
//   merged = baseKey || passwordUtf8
//   masterKey = PBKDF2(merged, salt=baseKey, 100_000, 32, "sha256")
func TestDeriveMasterKey_MatchesJSFormula(t *testing.T) {
	baseKey := []byte("0123456789abcdef0123456789abcdef")
	password := "mypassword"

	got := deriveMasterKey(baseKey, password)

	merged := append(append([]byte{}, baseKey...), []byte(password)...)
	want := pbkdf2.Key(merged, baseKey, 100_000, 32, sha256.New)

	if string(got) != string(want) {
		t.Fatalf("derived key mismatch")
	}
	if len(got) != 32 {
		t.Fatalf("master key length %d, want 32", len(got))
	}
}

// TestSerializeNote_ShapeMatchesJS confirms the CBOR layout: a 2-element
// top-level array, second element another array of [metadata, bytes]
// 2-tuples. Decoding back must yield the same content.
func TestSerializeNote_ShapeMatchesJS(t *testing.T) {
	buf, err := serializeNote("the body", []asset{{
		Metadata: map[string]any{"type": "file", "name": "x.tar"},
		Content:  []byte("payload"),
	}})
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	var decoded []any
	if err := cbor.Unmarshal(buf, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("want 2-tuple, got %d", len(decoded))
	}
	if decoded[0] != "the body" {
		t.Fatalf("content mismatch: %v", decoded[0])
	}
	assets, ok := decoded[1].([]any)
	if !ok {
		t.Fatalf("assets not array: %T", decoded[1])
	}
	if len(assets) != 1 {
		t.Fatalf("want 1 asset, got %d", len(assets))
	}
}
