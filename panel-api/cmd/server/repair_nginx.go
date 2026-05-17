package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ---------- nginx-config-invalid ----------
//
// Scar (incident 2026-05-15, mx.jabali-panel.com): commit 27ed1030 briefly
// emitted the `http2 on;` directive (valid only on nginx >= 1.25.1). It was
// reverted in d8d42cd1, but hosts that took a build in that window have a
// rendered jabali-default.conf / jabali-panel.conf containing `http2 on;`.
// `jabali update` pulls the corrected templates but NEVER re-renders these
// two server-scope files (the install.sh writers are always-overwrite but
// update does not re-run them) — so `nginx -t` stays broken forever, every
// reload is rejected, and nginx serves a stale config (self-signed default
// cert, :80 returns nothing). This detector self-heals that exact state.
//
// ADR-0077.

var managedNginxConfs = []string{
	"/etc/nginx/sites-available/jabali-default.conf",
	"/etc/nginx/sites-available/jabali-panel.conf",
}

var nginxVerRe = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// nginxVersionLT1251 reports whether the nginx version string denotes a
// release older than 1.25.1 — i.e. one where the standalone `http2 on;`
// directive is an "unknown directive" error and HTTP/2 must instead be a
// `listen ... http2` parameter. Unparseable input returns false
// (conservative: never auto-rewrite a config we cannot version-gate).
func nginxVersionLT1251(v string) bool {
	m := nginxVerRe.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	if maj != 1 {
		return maj < 1
	}
	if min != 25 {
		return min < 25
	}
	return pat < 1
}

// isSSLListen reports whether a trimmed line is a `listen ... ssl ...;`
// directive (HTTP/2 is only valid on a TLS listener — plain :80 listens
// must never gain http2).
func isSSLListen(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "listen ") || !strings.HasSuffix(trimmed, ";") {
		return false
	}
	ssl := false
	for _, f := range strings.Fields(strings.TrimSuffix(trimmed, ";")) {
		if f == "ssl" {
			ssl = true
		}
	}
	return ssl
}

// foldHTTP2 rewrites an nginx config so HTTP/2 is expressed via the
// portable `listen ... ssl http2;` parameter instead of the >=1.25.1-only
// standalone `http2 on;` directive: every standalone `http2 on;` line is
// dropped, and every ssl listen line that lacks an http2 token gains one.
// It is idempotent (already-correct input returns changed=false) and
// preserves indentation, ordering, and the trailing newline.
func foldHTTP2(in string) (string, bool) {
	lines := strings.Split(in, "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if trimmed == "http2 on;" {
			changed = true
			continue // drop the unsupported standalone directive
		}
		if isSSLListen(trimmed) && !strings.Contains(trimmed, "http2") {
			indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
			out = append(out, indent+strings.TrimSuffix(trimmed, ";")+" http2;")
			changed = true
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n"), changed
}

func nginxVersionString() string {
	out, _ := exec.Command("nginx", "-v").CombinedOutput()
	return string(out)
}

func detectNginxConfigInvalid(_ repairCtx) (bool, string, error) {
	if _, err := exec.LookPath("nginx"); err != nil {
		return false, "", nil // no nginx on this host — not applicable
	}
	if !nginxVersionLT1251(nginxVersionString()) {
		return false, "", nil // >=1.25.1: `http2 on;` is valid, leave it
	}
	var bad []string
	for _, f := range managedNginxConfs {
		b, err := os.ReadFile(f)
		if err != nil {
			continue // file absent — nothing to heal here
		}
		for _, ln := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(ln) == "http2 on;" {
				bad = append(bad, f)
				break
			}
		}
	}
	if len(bad) == 0 {
		return false, "", nil
	}
	return true, fmt.Sprintf("`http2 on;` (nginx<1.25.1 unknown directive) in: %s — nginx -t fails, reloads rejected", strings.Join(bad, ", ")), nil
}

func fixNginxConfigInvalid(_ repairCtx) error {
	for _, f := range managedNginxConfs {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		folded, changed := foldHTTP2(string(b))
		if !changed {
			continue
		}
		fi, err := os.Stat(f)
		mode := os.FileMode(0o644)
		if err == nil {
			mode = fi.Mode().Perm()
		}
		if err := os.WriteFile(f, []byte(folded), mode); err != nil {
			return fmt.Errorf("rewrite %s: %w", f, err)
		}
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		return fmt.Errorf("nginx -t still failing after fold (not reloading):\n%s", string(out))
	}
	if out, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); err != nil {
		return fmt.Errorf("nginx -t passed but reload failed: %v\n%s", err, string(out))
	}
	return nil
}
