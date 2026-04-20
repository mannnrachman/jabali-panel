package hydraclient

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// TestScopeLabels_AllShortAndLongSet guards the copy-consistency rule:
// every label must have a short noun phrase AND a long sentence. A
// regression that ships an empty Long field would render a scope on
// the consent card with just a bullet and no explanation.
func TestScopeLabels_AllShortAndLongSet(t *testing.T) {
	for _, l := range ScopeLabels {
		if l.Short == "" {
			t.Errorf("scope %q missing Short label", l.Scope)
		}
		if l.Long == "" {
			t.Errorf("scope %q missing Long label", l.Scope)
		}
	}
}

// TestScopeLabels_LongStartsWithLetThisApp enforces the consent-copy
// style rule documented on the ScopeLabel type. Keeping every sentence
// in the same voice makes the stacked list on the consent card read
// consistently; a stray label that breaks the pattern stands out
// awkwardly. If copy style ever intentionally changes, update this
// test + the comment together.
func TestScopeLabels_LongStartsWithLetThisApp(t *testing.T) {
	re := regexp.MustCompile(`^Let this app `)
	for _, l := range ScopeLabels {
		if !re.MatchString(l.Long) {
			t.Errorf("scope %q: Long must start with 'Let this app ', got %q", l.Scope, l.Long)
		}
	}
}

// TestScopeLabels_NoDuplicates catches a copy-paste regression where
// two entries share the same Scope ID. Without this, LabelFor() would
// return the first match and the duplicated copy would silently
// shadow the second.
func TestScopeLabels_NoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range ScopeLabels {
		if seen[l.Scope] {
			t.Errorf("scope %q duplicated in ScopeLabels", l.Scope)
		}
		seen[l.Scope] = true
	}
}

// TestLabelFor_HitAndMiss exercises the (label, ok) contract. A miss
// MUST return the zero-value ScopeLabel + false, not a partially-
// populated label — consumers branch on ok and a non-zero label with
// ok=false would make that branch do the wrong thing.
func TestLabelFor_HitAndMiss(t *testing.T) {
	if l, ok := LabelFor("openid"); !ok || l.Scope != "openid" {
		t.Errorf("openid: expected hit, got (%+v, %v)", l, ok)
	}
	l, ok := LabelFor("not-a-real-scope")
	if ok {
		t.Errorf("unknown scope: expected miss, got ok=true")
	}
	if l != (ScopeLabel{}) {
		t.Errorf("unknown scope: expected zero-value label on miss, got %+v", l)
	}
}

// TestKnownScopes_MatchesScopeLabels is a pure consistency check — if
// KnownScopes() drifts from ScopeLabels, config validation against a
// stale list would silently let new scopes through unreviewed.
func TestKnownScopes_MatchesScopeLabels(t *testing.T) {
	known := KnownScopes()
	if len(known) != len(ScopeLabels) {
		t.Fatalf("KnownScopes len=%d but ScopeLabels len=%d", len(known), len(ScopeLabels))
	}
	for i, scope := range known {
		if scope != ScopeLabels[i].Scope {
			t.Errorf("KnownScopes[%d]=%q but ScopeLabels[%d].Scope=%q", i, scope, i, ScopeLabels[i].Scope)
		}
	}
}

// TestScopeLabels_CoverHydraAdvertisedScopes enforces the "map covers
// every scope Hydra issues" invariant called out in the plan and ADR
// R5. If hydra.yml.tmpl starts advertising a new scope in
// webfinger.oidc_discovery.supported_scope without a matching
// ScopeLabel entry, the consent UI would render "Allow raw-scope-name"
// with no copy — the user would approve something we haven't written
// English for, which is how least-privilege regressions ship.
//
// The test reads the vendored template (source of truth at install
// time), grep-parses the supported_scope block, and asserts every entry
// has a LabelFor() hit. Yes, grep-parsing YAML is crude; it's also
// dependency-free and this is a small invariant.
func TestScopeLabels_CoverHydraAdvertisedScopes(t *testing.T) {
	// Walk up from the package dir to find install/hydra.yml.tmpl. The
	// go test working dir is the package, not the repo root.
	path, err := findRepoFile("install/hydra.yml.tmpl")
	if err != nil {
		t.Fatalf("locate hydra.yml.tmpl: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	// Look for the supported_scope block inside oidc_discovery. The
	// template layout is fixed by install.sh (the block immediately
	// follows `oidc_discovery:`); this parser is precise to that shape.
	scopes := extractSupportedScopes(string(body))
	if len(scopes) == 0 {
		t.Fatal("parser found no supported_scope entries — template layout changed; update this test")
	}

	for _, s := range scopes {
		if _, ok := LabelFor(s); !ok {
			t.Errorf("hydra.yml.tmpl advertises scope %q but scope_labels.go has no entry", s)
		}
	}

	// Symmetric check: every label must correspond to an advertised
	// scope, otherwise we have copy for something Hydra won't issue.
	labeled := sort.StringSlice(KnownScopes())
	advertised := sort.StringSlice(scopes)
	labeled.Sort()
	advertised.Sort()
	for _, l := range labeled {
		found := false
		for _, a := range advertised {
			if a == l {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("scope_labels.go has entry for %q but hydra.yml.tmpl doesn't advertise it", l)
		}
	}
}

// findRepoFile walks up from the test's cwd until it finds a sibling
// named relPath. Tests run in the package dir; the repo root is some
// number of `..` levels up.
func findRepoFile(relPath string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := cwd; dir != "/" && dir != ""; dir = filepath.Dir(dir) {
		cand := filepath.Join(dir, relPath)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	return "", os.ErrNotExist
}

// extractSupportedScopes pulls scope names out of a YAML snippet
// shaped like:
//
//	oidc_discovery:
//	  supported_scope:
//	    - openid
//	    - email
//	    - profile
//
// It's intentionally strict — if the template shape changes, this
// returns empty and the calling test fails loudly. That's the right
// failure mode: better to break the test and force an update than
// to silently let a scope slip through.
func extractSupportedScopes(yaml string) []string {
	// Find `supported_scope:` (the only key named that in the file).
	// The block is a flat list of `- <scope>` lines until a non-list
	// line is reached.
	blockRE := regexp.MustCompile(`(?m)^    supported_scope:\s*$`)
	loc := blockRE.FindStringIndex(yaml)
	if loc == nil {
		return nil
	}
	rest := yaml[loc[1]:]
	lineRE := regexp.MustCompile(`(?m)^      - ([\w.-]+)\s*$`)
	matches := lineRE.FindAllStringSubmatch(rest, -1)

	// Stop at the first line that doesn't match `      - xxx` — if we
	// don't, we'd sweep past the supported_scope block into
	// supported_claims and mis-report.
	var out []string
	endRE := regexp.MustCompile(`(?m)^    \w+:\s*$`)
	endLoc := endRE.FindStringIndex(rest)
	cutoff := len(rest)
	if endLoc != nil {
		cutoff = endLoc[0]
	}
	for _, m := range matches {
		// Index of this match in rest is harder to get post-FindAllStringSubmatch.
		// Cheap filter: re-find each match's position and drop if past cutoff.
		idx := regexp.MustCompile(`(?m)^      - ` + regexp.QuoteMeta(m[1]) + `\s*$`).FindStringIndex(rest[:cutoff])
		if idx != nil && idx[0] < cutoff {
			out = append(out, m[1])
		}
	}
	return out
}
