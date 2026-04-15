// Package ids centralises ULID generation + validation for the panel.
//
// Why ULID not UUIDv4:
//   - Sortable by creation time (handy for database indexes and log scanning).
//   - Shorter textual form (26 chars vs 36) with no hyphens.
//   - Same 128 bits of entropy as UUIDv4 in the random component.
//
// The blueprint already uses ULID in the WSS frame envelope (§10); using them
// everywhere keeps one ID format across wire protocols, DB rows, and logs.
package ids

import (
	"crypto/rand"
	"regexp"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// ulidRE is the canonical Crockford-base32 form of a ULID: 26 uppercase
// characters from the alphabet 0-9, A-H, J, K, M, N, P-T, V-Z (excluding
// I, L, O, U to avoid visual ambiguity).
var ulidRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// A single monotonic reader is shared so IDs generated in the same millisecond
// are strictly increasing — important for stable ORDER BY id behaviour.
// ulid.NewMonotonicEntropy is not safe for concurrent use; we serialise access.
var (
	mu      sync.Mutex
	entropy = ulid.Monotonic(rand.Reader, 0)
)

// NewULID returns a new ULID in its canonical textual form.
//
// Panics only if the crypto/rand source fails, which is fatal anyway.
func NewULID() string {
	mu.Lock()
	defer mu.Unlock()
	return ulid.MustNew(ulid.Timestamp(time.Now()), entropy).String()
}

// IsValidULID reports whether s is a well-formed canonical ULID string.
func IsValidULID(s string) bool {
	return ulidRE.MatchString(s)
}
