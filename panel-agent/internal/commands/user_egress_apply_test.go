package commands

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// allExist makes every slice "present on host" so the renderer emits
// every non-off user.
func allExist(string) bool { return true }
func noneExist(string) bool { return false }

func TestRenderEgressNFT_EmitsHeaderAndDefaults(t *testing.T) {
	out := RenderEgressNFT(nil, CanonicalDefaults(), allExist)

	require.Contains(t, out, "table inet jabali_per_user {")
	require.Contains(t, out, "set default_loopback4")
	require.Contains(t, out, "127.0.0.0/8")
	require.Contains(t, out, "set default_loopback6")
	require.Contains(t, out, "fc00::/7")
	require.Contains(t, out, "elements = { 53, 80, 443, 587, 465, 25 }")
	require.Contains(t, out, "type cgroupsv2 : verdict")
	require.Contains(t, out, "socket cgroupv2 level 3 vmap @cgroup_to_chain")
}

func TestRenderEgressNFT_OffStateSkipped(t *testing.T) {
	users := []EgressUser{
		{Username: "alice", State: "off"},
		{Username: "bob", State: "enforced"},
	}
	out := RenderEgressNFT(users, CanonicalDefaults(), allExist)

	require.Contains(t, out, "alice: state=off — skipped")
	require.NotContains(t, out, "user_alice_drops")
	require.NotContains(t, out, "user_alice_off")
	require.Contains(t, out, "counter user_bob_drops")
	require.Contains(t, out, "chain user_bob_enforced")
	require.Contains(t, out, "counter name user_bob_drops drop")
}

func TestRenderEgressNFT_LearningEmitsLogAndAccept(t *testing.T) {
	users := []EgressUser{{Username: "carol", State: "learning"}}
	out := RenderEgressNFT(users, CanonicalDefaults(), allExist)

	require.Contains(t, out, "chain user_carol_learning")
	require.Contains(t, out, "limit rate 5/minute log prefix \"jabali-egress-learn-carol \"")
	require.Contains(t, out, "counter name user_carol_drops accept")
	require.NotContains(t, out, "counter name user_carol_drops drop")
}

func TestRenderEgressNFT_VmapKeyMatchesM18Topology(t *testing.T) {
	users := []EgressUser{{Username: "dave", State: "enforced"}}
	out := RenderEgressNFT(users, CanonicalDefaults(), allExist)

	require.Contains(t, out,
		`"jabali.slice/jabali-user.slice/jabali-user-dave.slice" : jump user_dave_enforced`,
		"vmap key must match the actual M18 cgroup topology — see VM verification 2026-04-29")
}

func TestRenderEgressNFT_MissingSliceSkipped(t *testing.T) {
	users := []EgressUser{{Username: "eve", State: "enforced"}}
	out := RenderEgressNFT(users, CanonicalDefaults(), noneExist)

	require.Contains(t, out, "eve: slice")
	require.Contains(t, out, "missing on host — skipped")
	require.NotContains(t, out, "user_eve_drops")
	require.NotContains(t, out, ": jump user_eve_enforced")
}

func TestRenderEgressNFT_AllowedExtraEmittedInChain(t *testing.T) {
	port := 443
	users := []EgressUser{{
		Username: "frank",
		State:    "enforced",
		AllowedExtra: []EgressExtra{
			{CIDR: "203.0.113.0/24", Port: &port, Protocol: "tcp", Comment: "monitoring"},
			{CIDR: "2001:db8::/32"},
		},
	}}
	out := RenderEgressNFT(users, CanonicalDefaults(), allExist)

	require.Contains(t, out, "ip daddr 203.0.113.0/24 tcp dport 443 accept comment \"monitoring\"")
	require.Contains(t, out, "ip6 daddr 2001:db8::/32 accept")
}

func TestRenderEgressNFT_DeterministicOrdering(t *testing.T) {
	a := []EgressUser{
		{Username: "zara", State: "enforced"},
		{Username: "alice", State: "enforced"},
	}
	b := []EgressUser{
		{Username: "alice", State: "enforced"},
		{Username: "zara", State: "enforced"},
	}
	require.Equal(t, RenderEgressNFT(a, CanonicalDefaults(), allExist),
		RenderEgressNFT(b, CanonicalDefaults(), allExist),
		"renderer must sort users so output is reproducible")
}

func TestParseNFTCounters_HappyPath(t *testing.T) {
	raw := []byte(`{
	  "nftables": [
	    {"metainfo": {"version":"1.1.3"}},
	    {"counter": {"family":"inet","table":"jabali_per_user","name":"user_alice_drops","packets":42,"bytes":1680}},
	    {"counter": {"family":"inet","table":"jabali_per_user","name":"user_bob_drops","packets":0,"bytes":0}},
	    {"counter": {"family":"inet","table":"other","name":"user_zara_drops","packets":99}}
	  ]
	}`)

	got, err := parseNFTCounters(raw)
	require.NoError(t, err)
	require.Len(t, got, 2, "counter from a different table must be filtered out")

	byName := map[string]userEgressCounter{}
	for _, c := range got {
		byName[c.Username] = c
	}
	require.Equal(t, uint64(42), byName["alice"].Packets)
	require.Equal(t, uint64(0), byName["bob"].Packets)
}

func TestExtractUsernameFromCounter_ShapeRequired(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"user_alice_drops":      {"alice", true},
		"user_z9-x_drops":       {"z9-x", true},
		"user__drops":           {"", false},
		"misc_alice_drops":      {"", false},
		"user_alice_misc":       {"", false},
		"":                      {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := extractUsernameFromCounter(name)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSlicePathFor_M18Format(t *testing.T) {
	require.Equal(t,
		"jabali.slice/jabali-user.slice/jabali-user-shukivaknin.slice",
		SlicePathFor("shukivaknin"))
}

func TestPortStrings(t *testing.T) {
	require.Equal(t, "53,80,443", portStrings([]int{53, 80, 443}))
	// guard against accidental empty-list panic
	require.True(t, !strings.Contains(portStrings(nil), ","))
}
