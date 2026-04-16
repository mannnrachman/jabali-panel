// Package redirects compiles the structured redirect fields on a Domain
// into nginx directive strings the agent drops into a server {} block.
//
// Contract:
//
//   - Whole-domain redirect wins: if RedirectAllTo is set, page redirects
//     are ignored entirely (single `return N $url;` directive).
//   - Page redirects compile to `location = /src { return N "url"; }`
//     so unrelated paths still hit the docroot.
//   - Destination URLs emit as double-quoted nginx strings with embedded
//     quotes and backslashes escaped; nginx still interpolates $vars.
package redirects

import (
	"fmt"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func Compile(d *models.Domain) string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	if d.RedirectAllTo != nil && *d.RedirectAllTo != "" {
		code := "301"
		if d.RedirectAllType != nil {
			code = *d.RedirectAllType
		}
		fmt.Fprintf(&b, "    return %s %s;\n", code, quoteNginxURL(*d.RedirectAllTo))
		return b.String()
	}
	for _, pr := range d.PageRedirects {
		fmt.Fprintf(&b, "    location = %s {\n        return %s %s;\n    }\n",
			quoteNginxLocation(pr.Source), pr.Type, quoteNginxURL(pr.Destination))
	}
	return b.String()
}

func quoteNginxURL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func quoteNginxLocation(s string) string {
	if strings.ContainsAny(s, " \t\"'\\") {
		return quoteNginxURL(s)
	}
	return s
}
