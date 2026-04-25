// Package enclosed implements the client side of the enclosed.cc note-
// sharing protocol. We use an operator-controlled deployment at
// https://enclosed.jabali-panel.com to ship redacted diagnostic bundles
// to the support team. Notes are end-to-end encrypted: the server stores
// only ciphertext + a noteId, and the URL fragment carries the key (and
// "pw:" sentinel when password-protected).
//
// Protocol reference: github.com/CorentinTh/enclosed packages/lib +
// packages/crypto. We re-implement the Go side so the agent can mint
// notes without a JS runtime.
package enclosed

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"golang.org/x/crypto/pbkdf2"
)

// Client uploads encrypted notes to an enclosed-compatible server.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient returns a Client with a 60s HTTP timeout. Pass the bare
// origin (no trailing slash); we append /api/notes etc.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// UploadResult captures everything the operator needs to share the note.
type UploadResult struct {
	NoteID   string `json:"note_id"`
	URL      string `json:"url"`
	Password string `json:"password"`
}

// UploadFile uploads a single file as a password-protected enclosed note.
// content is the raw file bytes (a tar in our case); fileName + mimeType
// are the metadata enclosed shows in its UI when the team decrypts.
//
// Returns the shareable URL and the password the operator must send
// alongside it. The password is required to decrypt because we set
// `password` in the key-derivation step.
func (c *Client) UploadFile(ctx context.Context, fileName, mimeType string, content []byte, ttlSeconds int) (UploadResult, error) {
	password := genPassword(20)
	baseKey := randomBytes(32)
	masterKey := deriveMasterKey(baseKey, password)

	// CBOR-serialize the note in the [content, assets] shape enclosed
	// expects. Empty content text + one asset carrying the diagnostic
	// tar — operator sees the descriptive content + a downloadable file
	// in the enclosed UI.
	noteBuf, err := serializeNote("Jabali diagnostic report", []asset{{
		Metadata: map[string]any{
			"type":     "file",
			"name":     fileName,
			"fileType": mimeType,
			"size":     len(content),
		},
		Content: content,
	}})
	if err != nil {
		return UploadResult{}, fmt.Errorf("cbor serialize: %w", err)
	}

	encryptedPayload, err := encryptAES256GCM(noteBuf, masterKey)
	if err != nil {
		return UploadResult{}, fmt.Errorf("aes-gcm encrypt: %w", err)
	}

	body := map[string]any{
		"payload":             encryptedPayload,
		"deleteAfterReading":  false,
		"encryptionAlgorithm": "aes-256-gcm",
		"serializationFormat": "cbor-array",
		"isPublic":            true,
	}
	if ttlSeconds > 0 {
		body["ttlInSeconds"] = ttlSeconds
	}

	noteID, err := c.postNote(ctx, body)
	if err != nil {
		return UploadResult{}, err
	}

	return UploadResult{
		NoteID:   noteID,
		URL:      fmt.Sprintf("%s/%s#pw:%s", c.BaseURL, noteID, base64URL(baseKey)),
		Password: password,
	}, nil
}

func (c *Client) postNote(ctx context.Context, body map[string]any) (string, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/notes", bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
	var parsed struct {
		NoteID string `json:"noteId"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("parse response: %w (raw: %s)", err, string(respBody))
	}
	if parsed.NoteID == "" {
		return "", fmt.Errorf("empty noteId in response: %s", string(respBody))
	}
	return parsed.NoteID, nil
}

// asset mirrors the JS-side {metadata, content} shape.
type asset struct {
	Metadata map[string]any
	Content  []byte
}

// serializeNote produces the byte buffer the JS client would build via
// `cbor-x.encode([content, assets.map(a => [a.metadata, a.content])])`.
// Two-element top-level array; second element is itself an array of
// 2-tuples (metadata-map, byte-string).
func serializeNote(content string, assets []asset) ([]byte, error) {
	tuples := make([][]any, 0, len(assets))
	for _, a := range assets {
		tuples = append(tuples, []any{a.Metadata, a.Content})
	}
	return cbor.Marshal([]any{content, tuples})
}

// deriveMasterKey matches packages/crypto/src/node/crypto.node.usecases.ts
// `deriveMasterKey`:
//
//	pbkdf2(baseKey || passwordUtf8, salt=baseKey, 100_000, 32, sha256)
//
// Note the salt IS the baseKey itself. When no password is set, the
// password buffer is empty so the input is just baseKey.
func deriveMasterKey(baseKey []byte, password string) []byte {
	merged := append(append([]byte{}, baseKey...), []byte(password)...)
	return pbkdf2.Key(merged, baseKey, 100_000, 32, sha256.New)
}

// encryptAES256GCM matches the JS `aes-256-gcm` encryption path:
//
//	iv = 12 random bytes
//	ct = AES-256-GCM(key, iv, plaintext)        (auth tag appended)
//	out = base64url(iv) + ":" + base64url(ct||tag)
func encryptAES256GCM(plaintext, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	iv := randomBytes(12)
	// gcm.Seal returns ciphertext||tag in one slice — same layout the JS
	// side rebuilds from cipher.update + cipher.final + getAuthTag.
	ctAndTag := gcm.Seal(nil, iv, plaintext, nil)
	return base64URL(iv) + ":" + base64URL(ctAndTag), nil
}

// genPassword returns a URL-safe high-entropy password. 20 bytes of
// random base64url = 27 chars. Long enough to brute-force-resist any
// realistic attacker even with PBKDF2 100k iterations.
func genPassword(nBytes int) string {
	return base64URL(randomBytes(nBytes))
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return b
}

func base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
