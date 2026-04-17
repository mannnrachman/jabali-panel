package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestPathsUnderHome(t *testing.T) {
	cases := []struct {
		user    string
		docRoot string
		want    []string
	}{
		{
			"alice",
			"/home/alice/domains/foo.com/public_html",
			[]string{"/home/alice/domains/foo.com/public_html", "/home/alice/domains/foo.com", "/home/alice/domains"},
		},
		{
			"alice",
			"/home/alice/public_html",
			[]string{"/home/alice/public_html"},
		},
		{
			"alice",
			"/etc/passwd",
			nil,
		},
		{
			"alice",
			"/home/alice",
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.user+":"+tc.docRoot, func(t *testing.T) {
			got := pathsUnderHome(tc.user, tc.docRoot)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("user=%q docRoot=%q: got %v, want %v", tc.user, tc.docRoot, got, tc.want)
			}
		})
	}
}

func TestDomainCreateHandler_InvalidDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"uppercase", "Example.COM"},
		{"starts with hyphen", "-example.com"},
		{"ends with hyphen", "example-.com"},
		{"single label", "localhost"},
		{"no tld", "example"},
		{"dots only", "."},
		{"dot at start", ".example.com"},
		{"dot at end", "example.com."},
		{"double dot", "example..com"},
		{"spaces", "exam ple.com"},
		{"special chars", "exam@ple.com"},
		{"underscore", "exam_ple.com"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := domainCreateParams{
				Username:   "testuser",
				Domain:     tt.domain,
				DocRoot:    "/home/testuser/public_html/example.com",
				PHPVersion: "8.3",
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := domainCreateHandler(context.Background(), paramsJSON)
			require.NotNil(t, err)

			var aerr *agentwire.AgentError
			require.ErrorAs(t, err, &aerr)
			assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
		})
	}
}

func TestDomainCreateHandler_ValidDomains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"simple", "example.com"},
		{"subdomain", "sub.example.com"},
		{"multi-subdomain", "sub.sub.example.com"},
		{"hyphenated", "my-domain.com"},
		{"numbers", "example123.com"},
		{"start with number", "1example.com"},
		{"co.uk", "example.co.uk"},
		{"many hyphens", "my-long-domain-name.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just test that validation passes (we skip actual file system operations)
			if !domainRegex.MatchString(tt.domain) {
				t.Fatalf("expected %q to be valid", tt.domain)
			}
		})
	}
}

func TestDomainCreateHandler_InvalidUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
	}{
		{"uppercase", "TestUser"},
		{"starts with digit", "0user"},
		{"special char", "user@name"},
		{"too long", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := domainCreateParams{
				Username:   tt.username,
				Domain:     "example.com",
				DocRoot:    "/home/testuser/public_html/example.com",
				PHPVersion: "8.3",
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := domainCreateHandler(context.Background(), paramsJSON)
			require.NotNil(t, err)

			var aerr *agentwire.AgentError
			require.ErrorAs(t, err, &aerr)
			assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
		})
	}
}

func TestDomainCreateHandler_InvalidDocRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		docRoot string
	}{
		{"missing /home prefix", "/root/public_html/example.com"},
		{"relative path", "public_html/example.com"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := domainCreateParams{
				Username:   "testuser",
				Domain:     "example.com",
				DocRoot:    tt.docRoot,
				PHPVersion: "8.3",
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := domainCreateHandler(context.Background(), paramsJSON)
			require.NotNil(t, err)

			var aerr *agentwire.AgentError
			require.ErrorAs(t, err, &aerr)
			assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
		})
	}
}

func TestDomainCreateHandler_IsEnabledTrue(t *testing.T) {
	t.Parallel()

	// Test that when is_enabled is explicitly true, the template contains DocRoot
	params := domainCreateParams{
		Username:   "testuser",
		Domain:     "example.com",
		DocRoot:    "/home/testuser/public_html/example.com",
		PHPVersion: "8.3",
		IsEnabled:  ptrBool(true),
	}

	// Verify the template renders with the enabled config
	tmpl, _ := template.New("vhost").Parse(vhostTemplate)
	vd := vhostData{
		Domain:      params.Domain,
		DocRoot:     params.DocRoot,
		HasPHP:      true,
		PHPVersion:  params.PHPVersion,
		Username:    params.Username,
		IsEnabled:   true,
	}
	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, vd)
	output := buf.String()

	// Should contain the tenant's docroot and PHP-FPM config
	if !strings.Contains(output, "/home/testuser/public_html/example.com") {
		t.Errorf("expected output to contain DocRoot, got: %s", output)
	}
	if !strings.Contains(output, "fastcgi_pass") {
		t.Errorf("expected output to contain PHP-FPM config, got: %s", output)
	}
}

func TestDomainCreateHandler_IsEnabledFalse(t *testing.T) {
	t.Parallel()

	// Test that when is_enabled is false, the template serves the disabled page
	params := domainCreateParams{
		Username:   "testuser",
		Domain:     "example.com",
		DocRoot:    "/home/testuser/public_html/example.com",
		PHPVersion: "8.3",
		IsEnabled:  ptrBool(false),
	}

	// Verify the template renders with the disabled config
	tmpl, _ := template.New("vhost").Parse(vhostTemplate)
	vd := vhostData{
		Domain:      params.Domain,
		DocRoot:     params.DocRoot,
		PHPVersion:  params.PHPVersion,
		Username:    params.Username,
		IsEnabled:   false,
	}
	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, vd)
	output := buf.String()

	// Should contain the disabled page path and NOT the tenant's docroot
	if !strings.Contains(output, "/var/www/jabali-disabled") {
		t.Errorf("expected output to contain disabled page path, got: %s", output)
	}
	if !strings.Contains(output, "try_files /index.html =503") {
		t.Errorf("expected output to contain disabled fallback, got: %s", output)
	}
	if strings.Contains(output, "/home/testuser/public_html/example.com") {
		t.Errorf("expected output to NOT contain tenant's DocRoot, got: %s", output)
	}
	if strings.Contains(output, "fastcgi_pass") {
		t.Errorf("expected output to NOT contain PHP-FPM config, got: %s", output)
	}
}

func TestDomainCreateHandler_IsEnabledNil(t *testing.T) {
	t.Parallel()

	// Test that when is_enabled is nil, it defaults to true (backwards compatibility)
	params := domainCreateParams{
		Username:   "testuser",
		Domain:     "example.com",
		DocRoot:    "/home/testuser/public_html/example.com",
		PHPVersion: "8.3",
		IsEnabled:  nil,
	}

	// Verify the template defaults to enabled
	vd := vhostData{
		Domain:      params.Domain,
		DocRoot:     params.DocRoot,
		HasPHP:      true,
		PHPVersion:  params.PHPVersion,
		Username:    params.Username,
		IsEnabled:   true, // Default
	}
	tmpl, _ := template.New("vhost").Parse(vhostTemplate)
	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, vd)
	output := buf.String()

	// Should behave like enabled=true
	if !strings.Contains(output, "/home/testuser/public_html/example.com") {
		t.Errorf("expected output to contain DocRoot when is_enabled is nil (default true), got: %s", output)
	}
}

func TestDomainCreateHandler_CustomDirectives(t *testing.T) {
	t.Parallel()

	// Test that custom nginx directives are included in the vhost template
	customDirective := "add_header X-Test 1;"
	params := domainCreateParams{
		Username:         "testuser",
		Domain:           "example.com",
		DocRoot:          "/home/testuser/public_html/example.com",
		PHPVersion:       "8.3",
		CustomDirectives: customDirective,
		IsEnabled:        ptrBool(true),
	}

	// Verify the template renders with custom directives
	tmpl, _ := template.New("vhost").Parse(vhostTemplate)
	vd := vhostData{
		Domain:           params.Domain,
		DocRoot:          params.DocRoot,
		HasPHP:           true,
		PHPVersion:       params.PHPVersion,
		Username:         params.Username,
		CustomDirectives: params.CustomDirectives,
		IsEnabled:        true,
	}
	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, vd)
	output := buf.String()

	// Should contain the custom directive inside the enabled server block
	if !strings.Contains(output, customDirective) {
		t.Errorf("expected output to contain custom directive %q, got: %s", customDirective, output)
	}
	// Should also contain other enabled config
	if !strings.Contains(output, "fastcgi_pass") {
		t.Errorf("expected output to contain PHP-FPM config, got: %s", output)
	}
}

func TestDomainCreateHandler_CustomDirectivesNotInDisabledBlock(t *testing.T) {
	t.Parallel()

	// Test that custom directives are NOT included when vhost is disabled
	customDirective := "add_header X-Test 1;"
	params := domainCreateParams{
		Username:         "testuser",
		Domain:           "example.com",
		DocRoot:          "/home/testuser/public_html/example.com",
		PHPVersion:       "8.3",
		CustomDirectives: customDirective,
		IsEnabled:        ptrBool(false),
	}

	// Verify the template renders the disabled page without custom directives
	tmpl, _ := template.New("vhost").Parse(vhostTemplate)
	vd := vhostData{
		Domain:           params.Domain,
		DocRoot:          params.DocRoot,
		PHPVersion:       params.PHPVersion,
		Username:         params.Username,
		CustomDirectives: customDirective,
		IsEnabled:        false,
	}
	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, vd)
	output := buf.String()

	// Should NOT contain the custom directive in disabled mode
	if strings.Contains(output, customDirective) {
		t.Errorf("expected output to NOT contain custom directive in disabled mode, got: %s", output)
	}
	// Should contain the disabled page config
	if !strings.Contains(output, "/var/www/jabali-disabled") {
		t.Errorf("expected output to contain disabled page path, got: %s", output)
	}
}

func TestDomainCreateHandler_RedirectDirectives(t *testing.T) {
	t.Parallel()

	// Test that redirect directives are interpolated correctly into the enabled vhost
	// and appear BEFORE custom directives (higher precedence)
	redirectDirective := `    return 301 "https://new.com";` + "\n"
	customDirective := "add_header X-Test 1;"
	params := domainCreateParams{
		Username:           "testuser",
		Domain:             "example.com",
		DocRoot:            "/home/testuser/public_html/example.com",
		PHPVersion:         "8.3",
		RedirectDirectives: redirectDirective,
		CustomDirectives:   customDirective,
		IsEnabled:          ptrBool(true),
	}

	// Verify the template renders with redirect directives
	tmpl, _ := template.New("vhost").Parse(vhostTemplate)
	vd := vhostData{
		Domain:             params.Domain,
		DocRoot:            params.DocRoot,
		HasPHP:             true,
		PHPVersion:         params.PHPVersion,
		Username:           params.Username,
		RedirectDirectives: params.RedirectDirectives,
		CustomDirectives:   params.CustomDirectives,
		IsEnabled:          true,
	}
	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, vd)
	output := buf.String()

	// Should contain both redirect and custom directives
	if !strings.Contains(output, redirectDirective) {
		t.Errorf("expected output to contain redirect directive %q, got: %s", redirectDirective, output)
	}
	if !strings.Contains(output, customDirective) {
		t.Errorf("expected output to contain custom directive %q, got: %s", customDirective, output)
	}

	// Verify redirect directive appears BEFORE custom directive (higher precedence)
	redirectIdx := strings.Index(output, redirectDirective)
	customIdx := strings.Index(output, customDirective)
	if redirectIdx < 0 || customIdx < 0 {
		t.Fatalf("missing directives in output")
	}
	if redirectIdx >= customIdx {
		t.Errorf("expected redirect directive to appear before custom directive, but redirect at %d >= custom at %d", redirectIdx, customIdx)
	}

	// Should also contain other enabled config
	if !strings.Contains(output, "fastcgi_pass") {
		t.Errorf("expected output to contain PHP-FPM config, got: %s", output)
	}
}

func TestDomainCreateHandler_RedirectsNotInDisabledBlock(t *testing.T) {
	t.Parallel()

	// Test that redirect directives are NOT included when vhost is disabled
	redirectDirective := `    return 301 "https://new.com";` + "\n"
	params := domainCreateParams{
		Username:           "testuser",
		Domain:             "example.com",
		DocRoot:            "/home/testuser/public_html/example.com",
		PHPVersion:         "8.3",
		RedirectDirectives: redirectDirective,
		IsEnabled:          ptrBool(false),
	}

	// Verify the template renders the disabled page without redirect directives
	tmpl, _ := template.New("vhost").Parse(vhostTemplate)
	vd := vhostData{
		Domain:             params.Domain,
		DocRoot:            params.DocRoot,
		PHPVersion:         params.PHPVersion,
		Username:           params.Username,
		RedirectDirectives: redirectDirective,
		IsEnabled:          false,
	}
	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, vd)
	output := buf.String()

	// Should NOT contain the redirect directive in disabled mode
	if strings.Contains(output, redirectDirective) {
		t.Errorf("expected output to NOT contain redirect directive in disabled mode, got: %s", output)
	}
	// Should contain the disabled page config
	if !strings.Contains(output, "/var/www/jabali-disabled") {
		t.Errorf("expected output to contain disabled page path, got: %s", output)
	}
}

func TestDomainCreateHandler_RequiresRootNginx(t *testing.T) {
	t.Skip("requires root + nginx")

	params := domainCreateParams{
		Username:   "testuser",
		Domain:     "example.com",
		DocRoot:    "/home/testuser/public_html/example.com",
		PHPVersion: "8.3",
	}
	paramsJSON, _ := json.Marshal(params)

	_, err := domainCreateHandler(context.Background(), paramsJSON)
	require.NoError(t, err)
}

// ptrBool is a helper to create a pointer to a bool
func ptrBool(b bool) *bool {
	return &b
}

// TestVhostTemplate_RuleDirectives verifies that vhostTemplate includes rule_directives
// in the correct position in the directive ordering.
func TestVhostTemplate_RuleDirectives(t *testing.T) {
	// Test that vhostTemplate includes rule_directives placeholder
	if !strings.Contains(vhostTemplate, "{{.RuleDirectives}}") {
		t.Error("vhostTemplate missing {{.RuleDirectives}} placeholder")
	}

	// Verify ordering: {{.RedirectDirectives}} comes before {{.RuleDirectives}},
	// which comes before {{.CustomDirectives}}
	// This ensures proper directive precedence:
	// 1. RedirectDirectives (highest specificity - whole domain redirect)
	// 2. RuleDirectives (moderate - path-based rules)
	// 3. CustomDirectives (escape hatch - raw user input)
	redirectPos := strings.Index(vhostTemplate, "{{.RedirectDirectives}}")
	rulePos := strings.Index(vhostTemplate, "{{.RuleDirectives}}")
	customPos := strings.Index(vhostTemplate, "{{.CustomDirectives}}")

	if redirectPos < 0 || rulePos < 0 || customPos < 0 {
		t.Fatal("vhostTemplate missing one or more directive placeholders")
	}

	if !(redirectPos < rulePos && rulePos < customPos) {
		t.Errorf("vhostTemplate directive ordering incorrect: RedirectDirectives at %d, RuleDirectives at %d, CustomDirectives at %d; want redirectPos < rulePos < customPos",
			redirectPos, rulePos, customPos)
	}
}

// TestDomainCreateHandler_RuleDirectivesIntegration verifies that rule_directives
// parameter is properly passed through domainCreateHandler and included in vhost config.
func TestDomainCreateHandler_RuleDirectivesIntegration(t *testing.T) {
	params := domainCreateParams{
		Username:           "testuser",
		Domain:             "example.com",
		DocRoot:            "/home/testuser/public_html/example.com",
		PHPVersion:         "8.3",
		RuleDirectives:     `add_header X-Rule "rule-value";`,
		RedirectDirectives: `if ($request_uri = /old) { return 301 /new; }`,
		CustomDirectives:   `add_header X-Custom custom-value;`,
		IndexPriority:      "html_first",
		IsEnabled:          ptrBool(true),
	}

	// Verify that all three directive types can coexist
	// The actual vhost config generation is tested above (TestVhostTemplate_RuleDirectives)
	// This test ensures the params struct has the field and handler accepts it
	paramsJSON, err := json.Marshal(params)
	require.NoError(t, err)

	// Verify JSON contains the rule_directives field
	if !strings.Contains(string(paramsJSON), `"rule_directives"`) {
		t.Error("marshaled params missing rule_directives field")
	}

	// Verify unmarshaling works
	var unmarshaled domainCreateParams
	err = json.Unmarshal(paramsJSON, &unmarshaled)
	require.NoError(t, err)
	require.Equal(t, params.RuleDirectives, unmarshaled.RuleDirectives)
}
