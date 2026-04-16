// Package nginxrules compiles the structured NginxRule entries on a
// Domain into nginx directive strings that sit inside the server {}
// block. See the validator in internal/api for input constraints.
//
// Unknown rule types are silently skipped so an older panel build
// against a newer DB row never hard-fails nginx -t; the tradeoff is
// you won't see an error, just a missing directive.
package nginxrules

import (
	"fmt"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func Compile(d *models.Domain) string {
	if d == nil || len(d.NginxRules) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range d.NginxRules {
		switch r.Type {
		case "custom_header":
			alwaysSuffix := ""
			if r.Always != nil && *r.Always {
				alwaysSuffix = " always"
			}
			fmt.Fprintf(&b, "    add_header %s %s%s;\n",
				r.Name, quoteNginxString(r.Value), alwaysSuffix)

		case "rewrite":
			flag := r.Flag
			if flag == "" {
				flag = "last"
			}
			fmt.Fprintf(&b, "    rewrite %s %s %s;\n",
				r.Pattern, quoteNginxString(r.Replacement), flag)

		case "proxy_pass":
			fmt.Fprintf(&b,
				"    location %s {\n"+
					"        proxy_pass %s;\n"+
					"        proxy_set_header Host $host;\n"+
					"        proxy_set_header X-Real-IP $remote_addr;\n"+
					"        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n"+
					"        proxy_set_header X-Forwarded-Proto $scheme;\n"+
					"    }\n",
				quoteNginxLocation(r.Path), r.Target)

		case "ip_access":
			b.WriteString("    location ")
			b.WriteString(quoteNginxLocation(r.Path))
			b.WriteString(" {\n")
			switch r.Mode {
			case "deny_list":
				for _, ip := range r.IPs {
					fmt.Fprintf(&b, "        deny %s;\n", ip)
				}
				b.WriteString("        allow all;\n")
			default: // allow_list
				for _, ip := range r.IPs {
					fmt.Fprintf(&b, "        allow %s;\n", ip)
				}
				b.WriteString("        deny all;\n")
			}
			b.WriteString("    }\n")

		case "php_setting":
			// Injected via fastcgi_param so it reaches PHP-FPM.
			fmt.Fprintf(&b,
				"    fastcgi_param PHP_VALUE %s;\n",
				quoteNginxString(r.Name+"="+r.Value))

		case "max_upload_size":
			fmt.Fprintf(&b, "    client_max_body_size %s;\n", r.Size)
		}
	}
	return b.String()
}

func quoteNginxString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func quoteNginxLocation(s string) string {
	if strings.ContainsAny(s, " \t\"'\\") {
		return quoteNginxString(s)
	}
	return s
}
