package redirects

import (
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

func TestCompile(t *testing.T) {
	tests := []struct {
		name     string
		domain   *models.Domain
		expected string
	}{
		{
			name:     "nil domain",
			domain:   nil,
			expected: "",
		},
		{
			name:     "no redirects configured",
			domain:   &models.Domain{},
			expected: "",
		},
		{
			name: "whole-domain 301",
			domain: &models.Domain{
				RedirectAllTo:   str("https://new.com"),
				RedirectAllType: str("301"),
			},
			expected: `    return 301 "https://new.com";
`,
		},
		{
			name: "whole-domain defaults to 301 when type is nil",
			domain: &models.Domain{
				RedirectAllTo: str("https://new.com"),
			},
			expected: `    return 301 "https://new.com";
`,
		},
		{
			name: "whole-domain 302 output",
			domain: &models.Domain{
				RedirectAllTo:   str("https://new.com"),
				RedirectAllType: str("302"),
			},
			expected: `    return 302 "https://new.com";
`,
		},
		{
			name: "whole-domain SUPERSEDES page redirects",
			domain: &models.Domain{
				RedirectAllTo:   str("https://new.com"),
				RedirectAllType: str("301"),
				PageRedirects: models.PageRedirects{
					models.PageRedirect{Source: "/old", Destination: "https://other.com", Type: "301"},
				},
			},
			expected: `    return 301 "https://new.com";
`,
		},
		{
			name: "single page redirect exact-match output",
			domain: &models.Domain{
				PageRedirects: models.PageRedirects{
					models.PageRedirect{Source: "/old", Destination: "https://new.com/page", Type: "301"},
				},
			},
			expected: `    location = /old {
        return 301 "https://new.com/page";
    }
`,
		},
		{
			name: "embedded double-quote in destination (escaped)",
			domain: &models.Domain{
				RedirectAllTo:   str(`https://new.com/path?q="value"`),
				RedirectAllType: str("301"),
			},
			expected: `    return 301 "https://new.com/path?q=\"value\"";
`,
		},
		{
			name: "nginx $vars in destination preserved",
			domain: &models.Domain{
				PageRedirects: models.PageRedirects{
					models.PageRedirect{Source: "/old", Destination: "https://new.com/$request_uri", Type: "301"},
				},
			},
			expected: `    location = /old {
        return 301 "https://new.com/$request_uri";
    }
`,
		},
		{
			name: "multiple page redirects",
			domain: &models.Domain{
				PageRedirects: models.PageRedirects{
					models.PageRedirect{Source: "/old1", Destination: "https://new.com/page1", Type: "301"},
					models.PageRedirect{Source: "/old2", Destination: "https://new.com/page2", Type: "302"},
				},
			},
			expected: `    location = /old1 {
        return 301 "https://new.com/page1";
    }
    location = /old2 {
        return 302 "https://new.com/page2";
    }
`,
		},
		{
			name: "backslash escaping in destination",
			domain: &models.Domain{
				RedirectAllTo:   str(`https://new.com\path`),
				RedirectAllType: str("301"),
			},
			expected: `    return 301 "https://new.com\\path";
`,
		},
		{
			name: "both quote and backslash in destination",
			domain: &models.Domain{
				RedirectAllTo:   str(`https://new.com\path?q="value"`),
				RedirectAllType: str("301"),
			},
			expected: `    return 301 "https://new.com\\path?q=\"value\"";
`,
		},
		{
			name: "location path with spaces (quoted)",
			domain: &models.Domain{
				PageRedirects: models.PageRedirects{
					models.PageRedirect{Source: "/my path", Destination: "https://new.com/page", Type: "301"},
				},
			},
			expected: `    location = "/my path" {
        return 301 "https://new.com/page";
    }
`,
		},
		{
			name: "empty RedirectAllTo cleared (nil after trim)",
			domain: &models.Domain{
				RedirectAllTo: str(""),
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compile(tt.domain)
			if got != tt.expected {
				t.Errorf("Compile() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func str(s string) *string {
	return &s
}
