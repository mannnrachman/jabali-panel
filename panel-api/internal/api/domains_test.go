package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/ginctx"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/repository"
)

func TestValidateNginxDirectives(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
		errMsg    string
	}{
		{
			name:      "empty string",
			input:     "",
			wantError: false,
		},
		{
			name:      "whitespace only",
			input:     "   \n  \t  \n",
			wantError: false,
		},
		{
			name:      "comment only",
			input:     "# this is a comment",
			wantError: false,
		},
		{
			name:      "multiple comment lines",
			input:     "# comment 1\n# comment 2\n# comment 3",
			wantError: false,
		},
		{
			name:      "simple add_header",
			input:     `add_header X-Foo "bar";`,
			wantError: false,
		},
		{
			name:      "proxy_pass basic",
			input:     "proxy_pass http://localhost:3000;",
			wantError: false,
		},
		{
			name:      "proxy_pass with trailing comment",
			input:     "proxy_pass http://localhost:3000; # forward traffic",
			wantError: false,
		},
		{
			name:      "multiple valid directives",
			input:     "add_header X-Foo bar;\nproxy_pass http://localhost:3000;",
			wantError: false,
		},
		{
			name:      "hash inside double-quoted string is not a comment",
			input:     `add_header X-Foo "value with # hash";`,
			wantError: false,
		},
		{
			name:      "multiple directives with hash in string",
			input:     `add_header X-Foo "# not a comment";\nadd_header X-Bar baz;`,
			wantError: false,
		},
		{
			name:      "hash inside single-quoted string is not a comment",
			input:     `set $var 'value with # hash';`,
			wantError: false,
		},
		{
			name:      "mixed case directive (case-insensitive)",
			input:     "AdD_HeAdEr X-Foo bar;",
			wantError: false,
		},
		{
			name:      "mixed case proxy_pass",
			input:     "PROXY_PASS http://localhost:3000;",
			wantError: false,
		},
		{
			name:      "try_files directive",
			input:     "try_files $uri /index.php?$query_string;",
			wantError: false,
		},
		{
			name:      "rewrite directive",
			input:     "rewrite ^/old/(.*) /new/$1 permanent;",
			wantError: false,
		},
		{
			name:      "if block with directives",
			input:     "if ($request_method = POST) {\nreturn 405;\n}",
			wantError: false,
		},
		{
			name:      "location block",
			input:     "location /api {\nproxy_pass http://backend;\n}",
			wantError: false,
		},
		{
			name:      "nested location",
			input:     "location / {\nif ($condition) {\nreturn 200;\n}\n}",
			wantError: false,
		},
		{
			name:      "three levels of nesting",
			input:     "location / {\nif ($cond) {\nlimit_except GET HEAD {\ndeny all;\n}\n}\n}",
			wantError: false,
		},
		{
			name:      "forbidden: include /etc/passwd",
			input:     "include /etc/passwd;",
			wantError: true,
			errMsg:    "forbidden directive: include",
		},
		{
			name:      "forbidden: root /etc",
			input:     "root /etc;",
			wantError: true,
			errMsg:    "forbidden directive: root",
		},
		{
			name:      "forbidden: alias /etc/",
			input:     "alias /etc/;",
			wantError: true,
			errMsg:    "forbidden directive: alias",
		},
		{
			name:      "forbidden: access_log off",
			input:     "access_log off;",
			wantError: true,
			errMsg:    "forbidden directive: access_log",
		},
		{
			name:      "forbidden: access_log /var/log/nginx/access.log",
			input:     "access_log /var/log/nginx/access.log;",
			wantError: true,
			errMsg:    "forbidden directive: access_log",
		},
		{
			name:      "forbidden: error_log",
			input:     "error_log /var/log/nginx/error.log warn;",
			wantError: true,
			errMsg:    "forbidden directive: error_log",
		},
		{
			name:      "forbidden: ssl_certificate",
			input:     "ssl_certificate /etc/ssl/certs/example.com.crt;",
			wantError: true,
			errMsg:    "forbidden directive: ssl_certificate",
		},
		{
			name:      "forbidden: ssl_certificate_key",
			input:     "ssl_certificate_key /etc/ssl/private/example.com.key;",
			wantError: true,
			errMsg:    "forbidden directive: ssl_certificate_key",
		},
		{
			name:      "forbidden: load_module",
			input:     "load_module /usr/lib/nginx/modules/ngx_module.so;",
			wantError: true,
			errMsg:    "forbidden directive: load_module",
		},
		{
			name:      "forbidden: lua_code_cache off",
			input:     "lua_code_cache off;",
			wantError: true,
			errMsg:    "forbidden directive: lua_code_cache",
		},
		{
			name:      "forbidden: perl_modules",
			input:     "perl_modules /usr/share/perl/5.26;",
			wantError: true,
			errMsg:    "forbidden directive: perl_modules",
		},
		{
			name:      "forbidden: listen",
			input:     "listen 80;",
			wantError: true,
			errMsg:    "forbidden directive: listen",
		},
		{
			name:      "forbidden: server_name",
			input:     "server_name example.com;",
			wantError: true,
			errMsg:    "forbidden directive: server_name",
		},
		{
			name:      "forbidden: env VAR",
			input:     "env MY_VAR;",
			wantError: true,
			errMsg:    "forbidden directive: env",
		},
		{
			name:      "forbidden: user nobody",
			input:     "user nobody;",
			wantError: true,
			errMsg:    "forbidden directive: user",
		},
		{
			name:      "forbidden: worker_processes",
			input:     "worker_processes 4;",
			wantError: true,
			errMsg:    "forbidden directive: worker_processes",
		},
		{
			name:      "forbidden directive in middle of valid directives",
			input:     "add_header X-Foo bar;\nroot /etc;\nadd_header X-Baz qux;",
			wantError: true,
			errMsg:    "forbidden directive: root",
		},
		{
			name:      "unbalanced: extra closing brace",
			input:     "add_header X-Foo bar;\n}",
			wantError: true,
			errMsg:    "forbidden directive: unbalanced braces (extra closing })",
		},
		{
			name:      "unbalanced: unclosed opening brace",
			input:     "location / {",
			wantError: true,
			errMsg:    "forbidden directive: unbalanced braces (unclosed {)",
		},
		{
			name:      "nesting depth exceeded: 4 levels",
			input:     "location / {\nif ($c) {\nlimit_except GET {\nreturn 405;\nlocation /deep {\ndeny all;\n}\n}\n}\n}",
			wantError: true,
			errMsg:    "forbidden directive: nesting depth exceeded",
		},
		{
			name:      "null byte injection",
			input:     "add_header X-Foo bar\x00baz;",
			wantError: true,
			errMsg:    "forbidden directive: null byte detected",
		},
		{
			name:      "gzip directives",
			input:     "gzip on;\ngzip_types text/plain;\ngzip_comp_level 6;",
			wantError: false,
		},
		{
			name:      "limit_req_zone directive",
			input:     "limit_req_zone $binary_remote_addr zone=one:10m rate=10r/s;",
			wantError: false,
		},
		{
			name:      "fastcgi_param directive",
			input:     "fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;",
			wantError: false,
		},
		{
			name:      "auth_basic_user_file",
			input:     "auth_basic_user_file /etc/nginx/.htpasswd;",
			wantError: false,
		},
		{
			name:      "empty line between directives",
			input:     "add_header X-Foo bar;\n\nproxy_pass http://localhost:3000;",
			wantError: false,
		},
		{
			name:      "trailing whitespace and comments",
			input:     "add_header X-Foo bar;  \t  # comment\nproxy_pass http://localhost:3000; # another",
			wantError: false,
		},
		{
			name:      "quoted string with escaped quote",
			input:     `add_header X-Foo "bar\"baz";`,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNginxDirectives(tt.input)
			if (err != "") != tt.wantError {
				t.Errorf("validateNginxDirectives() error = %q, wantError %v", err, tt.wantError)
				return
			}
			if tt.wantError && tt.errMsg != "" && err != tt.errMsg {
				// Check if the expected message is a substring (for some error messages)
				if !contains(err, tt.errMsg) {
					t.Errorf("validateNginxDirectives() error = %q, want substring %q", err, tt.errMsg)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestStripComments(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "add_header X-Foo bar;",
			expected: "add_header X-Foo bar;",
		},
		{
			input:    "add_header X-Foo bar; # this is a comment",
			expected: "add_header X-Foo bar; ",
		},
		{
			input:    "# full line comment",
			expected: "",
		},
		{
			input:    `add_header X "value with # hash";`,
			expected: `add_header X "value with # hash";`,
		},
		{
			input:    `add_header X 'value with # hash';`,
			expected: `add_header X 'value with # hash';`,
		},
		{
			input:    `add_header X "quoted"; # comment`,
			expected: `add_header X "quoted"; `,
		},
		{
			input:    `set $x 'single'; # comment after single-quoted`,
			expected: `set $x 'single'; `,
		},
		{
			input:    "no comment here",
			expected: "no comment here",
		},
		{
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripComments(tt.input)
			if got != tt.expected {
				t.Errorf("stripComments(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractDirective(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "add_header X-Foo bar;",
			expected: "add_header",
		},
		{
			input:    "proxy_pass http://localhost:3000;",
			expected: "proxy_pass",
		},
		{
			input:    "   location   /api {",
			expected: "location",
		},
		{
			input:    "if ($cond) {",
			expected: "if",
		},
		{
			input:    "}",
			expected: "}",
		},
		{
			input:    "{",
			expected: "{",
		},
		{
			input:    "",
			expected: "",
		},
		{
			input:    "   ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractDirective(tt.input)
			if got != tt.expected {
				t.Errorf("extractDirective(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// mockDomainRepository is a mock implementation of repository.DomainRepository
type mockDomainRepository struct {
	mock.Mock
}

func (m *mockDomainRepository) List(ctx context.Context, opts repository.ListOptions) ([]models.Domain, int64, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]models.Domain), args.Get(1).(int64), args.Error(2)
}

func (m *mockDomainRepository) ListByUserID(ctx context.Context, userID string, opts repository.ListOptions) ([]models.Domain, int64, error) {
	args := m.Called(ctx, userID, opts)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]models.Domain), args.Get(1).(int64), args.Error(2)
}

func (m *mockDomainRepository) FindByID(ctx context.Context, id string) (*models.Domain, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Domain), args.Error(1)
}

func (m *mockDomainRepository) Create(ctx context.Context, domain *models.Domain) error {
	return m.Called(ctx, domain).Error(0)
}

func (m *mockDomainRepository) Update(ctx context.Context, domain *models.Domain) error {
	return m.Called(ctx, domain).Error(0)
}

func (m *mockDomainRepository) Delete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockDomainRepository) CountByUserID(ctx context.Context, userID string) (int64, error) {
	args := m.Called(ctx, userID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockDomainRepository) ExistsByUserIDAndName(ctx context.Context, userID string, name string) (bool, error) {
	args := m.Called(ctx, userID, name)
	return args.Get(0).(bool), args.Error(1)
}

// mockSSLCertificateRepository is a mock implementation of repository.SSLCertificateRepository
type mockSSLCertificateRepository struct {
	mock.Mock
}

func (m *mockSSLCertificateRepository) FindByDomainID(ctx context.Context, domainID string) (*models.SSLCertificate, error) {
	args := m.Called(ctx, domainID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.SSLCertificate), args.Error(1)
}

func (m *mockSSLCertificateRepository) FindByDomainIDs(ctx context.Context, domainIDs []string) ([]models.SSLCertificate, error) {
	args := m.Called(ctx, domainIDs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]models.SSLCertificate), args.Error(1)
}

func (m *mockSSLCertificateRepository) Create(ctx context.Context, cert *models.SSLCertificate) error {
	return m.Called(ctx, cert).Error(0)
}

func (m *mockSSLCertificateRepository) Update(ctx context.Context, cert *models.SSLCertificate) error {
	return m.Called(ctx, cert).Error(0)
}

func (m *mockSSLCertificateRepository) Delete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}

// TestDomainList_WithLetsEncryptCertificate tests domain list with issued Let's Encrypt certificate
func TestDomainList_WithLetsEncryptCertificate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().UTC()
	domainID := "domain-1"
	domain := models.Domain{
		ID:         domainID,
		UserID:     "user-1",
		Name:       "example.com",
		DocRoot:    "/home/user/domains/example.com/public_html",
		IsEnabled:  true,
		SSLEnabled: true,
	}

	cert := models.SSLCertificate{
		ID:        "cert-1",
		DomainID:  domainID,
		Status:    models.SSLStatusIssued,
		IssuedAt:  &now,
		ExpiresAt: &now.AddDate(0, 0, 90),
	}

	mockDomains := new(mockDomainRepository)
	mockSSL := new(mockSSLCertificateRepository)

	mockDomains.On("List", mock.Anything, mock.Anything).Return([]models.Domain{domain}, int64(1), nil)
	mockSSL.On("FindByDomainIDs", mock.Anything, []string{domainID}).Return([]models.SSLCertificate{cert}, nil)

	handler := &domainHandler{
		cfg: DomainHandlerConfig{
			Domains:  mockDomains,
			SSLCerts: mockSSL,
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/domains?page=1&page_size=20", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ginctx.ClaimsKey, &ginctx.Claims{
		UserID:  "user-1",
		IsAdmin: true,
	}))

	handler.list(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomains.AssertCalled(t, "List", mock.Anything, mock.Anything)
	mockSSL.AssertCalled(t, "FindByDomainIDs", mock.Anything, []string{domainID})
}

// TestDomainList_WithSelfSignedCertificate tests domain list with self-signed certificate
func TestDomainList_WithSelfSignedCertificate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now().UTC()
	domainID := "domain-2"
	domain := models.Domain{
		ID:         domainID,
		UserID:     "user-1",
		Name:       "test.local",
		DocRoot:    "/home/user/domains/test.local/public_html",
		IsEnabled:  true,
		SSLEnabled: true,
	}

	cert := models.SSLCertificate{
		ID:        "cert-2",
		DomainID:  domainID,
		Status:    models.SSLStatusSelfSigned,
		IssuedAt:  &now,
		ExpiresAt: &now.AddDate(1, 0, 0),
	}

	mockDomains := new(mockDomainRepository)
	mockSSL := new(mockSSLCertificateRepository)

	mockDomains.On("List", mock.Anything, mock.Anything).Return([]models.Domain{domain}, int64(1), nil)
	mockSSL.On("FindByDomainIDs", mock.Anything, []string{domainID}).Return([]models.SSLCertificate{cert}, nil)

	handler := &domainHandler{
		cfg: DomainHandlerConfig{
			Domains:  mockDomains,
			SSLCerts: mockSSL,
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/domains?page=1&page_size=20", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ginctx.ClaimsKey, &ginctx.Claims{
		UserID:  "user-1",
		IsAdmin: true,
	}))

	handler.list(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomains.AssertCalled(t, "List", mock.Anything, mock.Anything)
	mockSSL.AssertCalled(t, "FindByDomainIDs", mock.Anything, []string{domainID})
}

// TestDomainList_WithPendingCertificate tests domain list with pending certificate
func TestDomainList_WithPendingCertificate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	domainID := "domain-3"
	domain := models.Domain{
		ID:         domainID,
		UserID:     "user-1",
		Name:       "pending.example.com",
		DocRoot:    "/home/user/domains/pending.example.com/public_html",
		IsEnabled:  true,
		SSLEnabled: true,
	}

	cert := models.SSLCertificate{
		ID:       "cert-3",
		DomainID: domainID,
		Status:   models.SSLStatusPending,
	}

	mockDomains := new(mockDomainRepository)
	mockSSL := new(mockSSLCertificateRepository)

	mockDomains.On("List", mock.Anything, mock.Anything).Return([]models.Domain{domain}, int64(1), nil)
	mockSSL.On("FindByDomainIDs", mock.Anything, []string{domainID}).Return([]models.SSLCertificate{cert}, nil)

	handler := &domainHandler{
		cfg: DomainHandlerConfig{
			Domains:  mockDomains,
			SSLCerts: mockSSL,
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/domains?page=1&page_size=20", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ginctx.ClaimsKey, &ginctx.Claims{
		UserID:  "user-1",
		IsAdmin: true,
	}))

	handler.list(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomains.AssertCalled(t, "List", mock.Anything, mock.Anything)
	mockSSL.AssertCalled(t, "FindByDomainIDs", mock.Anything, []string{domainID})
}

// TestDomainList_NoCertificate tests domain list with no SSL certificate
func TestDomainList_NoCertificate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	domainID := "domain-4"
	domain := models.Domain{
		ID:         domainID,
		UserID:     "user-1",
		Name:       "nocert.example.com",
		DocRoot:    "/home/user/domains/nocert.example.com/public_html",
		IsEnabled:  true,
		SSLEnabled: false,
	}

	mockDomains := new(mockDomainRepository)
	mockSSL := new(mockSSLCertificateRepository)

	mockDomains.On("List", mock.Anything, mock.Anything).Return([]models.Domain{domain}, int64(1), nil)
	mockSSL.On("FindByDomainIDs", mock.Anything, []string{domainID}).Return([]models.SSLCertificate{}, nil)

	handler := &domainHandler{
		cfg: DomainHandlerConfig{
			Domains:  mockDomains,
			SSLCerts: mockSSL,
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/domains?page=1&page_size=20", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ginctx.ClaimsKey, &ginctx.Claims{
		UserID:  "user-1",
		IsAdmin: true,
	}))

	handler.list(c)

	assert.Equal(t, http.StatusOK, w.Code)
	mockDomains.AssertCalled(t, "List", mock.Anything, mock.Anything)
	mockSSL.AssertCalled(t, "FindByDomainIDs", mock.Anything, []string{domainID})
}
