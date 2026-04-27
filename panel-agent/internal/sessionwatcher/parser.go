package sessionwatcher

import (
	"regexp"
	"strconv"
	"strings"
)

// Parse extracts a Session from the raw text body of a maldet
// session.<id> file. Tolerant of missing fields — best-effort extract,
// never panic. Real maldet output looks like:
//
//	Linux Malware Detect v1.6.6
//	(C) 2002-2024, R-fx Networks <proj@rfxn.com>
//	(C) 2024, Ryan MacDonald <ryan@r-fx.org>
//	inotifywait <signal>: ...
//
//	scan of '/home/alice' (fileset: 1234, 0 eligible)
//	  TOTAL HITS: 3
//	  TOTAL CLEANED: 0
//	  HIT: {SIG} /home/alice/public_html/x.php
//	  HIT: {SIG} /home/alice/public_html/y.php
//	...
func Parse(text string) Session {
	s := Session{Raw: text}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "SCAN ID:"):
			s.ID = strings.TrimSpace(strings.TrimPrefix(line, "SCAN ID:"))
		case strings.HasPrefix(line, "TOTAL FILES:"):
			s.TotalFiles, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "TOTAL FILES:")))
		case strings.HasPrefix(line, "TOTAL HITS:"):
			s.TotalHits, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "TOTAL HITS:")))
		case strings.HasPrefix(line, "HIT:"):
			if h, ok := parseHitLine(strings.TrimPrefix(line, "HIT:")); ok {
				s.Hits = append(s.Hits, h)
			}
		}
	}
	return s
}

// parseHitLine extracts a Hit from one of the documented HIT line shapes.
// LMD has used multiple formats over the years; we support the two we
// see in 1.6.x output:
//
//	HIT: {HEX}php.malware.web_shell.001 /home/alice/public_html/x.php
//	HIT: php.malware.web_shell.001 : /home/alice/public_html/x.php
//
// SHA + size are not present on the HIT line itself; the agent
// enriches them via stat + sha256File before posting to panel-api.
var hitRE = regexp.MustCompile(`^\s*(?:\{[^}]+\})?\s*([A-Za-z0-9._:-]+)\s*[: ]\s*(/.+?)\s*$`)

func parseHitLine(rest string) (Hit, bool) {
	m := hitRE.FindStringSubmatch(rest)
	if len(m) != 3 {
		return Hit{}, false
	}
	return Hit{Signature: m[1], OriginalPath: m[2]}, true
}
