package ids_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ids"
)

// ULID canonical form is 26 chars Crockford-base32.
var ulidRE = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

func TestNewULID_ShapeAndUniqueness(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := ids.NewULID()
		require.Len(t, id, 26)
		assert.True(t, ulidRE.MatchString(id), "id %q must be valid ULID", id)

		_, dup := seen[id]
		require.False(t, dup, "duplicate id %q", id)
		seen[id] = struct{}{}
	}
}

func TestNewULID_MonotonicWithinMillisecond(t *testing.T) {
	// ULIDs generated back-to-back should be strictly ordered. We don't
	// assert strict equality to time since multiple IDs in a single ms may
	// increment the random suffix; just that sort order holds.
	t.Parallel()

	prev := ids.NewULID()
	for range 100 {
		cur := ids.NewULID()
		assert.Greater(t, cur, prev, "ULIDs should be sortable: %q -> %q", prev, cur)
		prev = cur
	}
}

func TestIsValidULID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"01HRCWR7CKMCBEDF2PYQ7G0D2J", true},
		{"01HRCWR7CKMCBEDF2PYQ7G0D2j", false}, // lowercase
		{"", false},
		{"too-short", false},
		{"01HRCWR7CKMCBEDF2PYQ7G0D2JXX", false}, // too long
		{"0IHRCWR7CKMCBEDF2PYQ7G0D2J", false},   // 'I' is invalid in Crockford base32
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, ids.IsValidULID(tc.in))
		})
	}
}
