package filesafe

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewScope validates scope creation and normalization.
func TestNewScope(t *testing.T) {
	cases := []struct {
		name      string
		userID    string
		username  string
		docroots  []string
		wantErr   bool
		errCode   string
	}{
		{
			name:     "valid single docroot",
			userID:   "u123",
			username: "shuki",
			docroots: []string{"/var/www"},
			wantErr:  false,
		},
		{
			name:     "valid multiple docroots",
			userID:   "u123",
			username: "shuki",
			docroots: []string{"/var/www", "/home/shuki"},
			wantErr:  false,
		},
		{
			name:     "empty username",
			userID:   "u123",
			username: "",
			docroots: []string{"/var/www"},
			wantErr:  true,
			errCode:  ErrCodeEmpty,
		},
		{
			name:     "empty docroots",
			userID:   "u123",
			username: "shuki",
			docroots: []string{},
			wantErr:  true,
			errCode:  ErrCodeEmpty,
		},
		{
			name:     "relative docroot",
			userID:   "u123",
			username: "shuki",
			docroots: []string{"relative/path"},
			wantErr:  true,
			errCode:  ErrCodeNotAbsolute,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, err := NewScope(tc.userID, tc.username, tc.docroots)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if ve, ok := err.(*ValidationError); ok {
					if ve.Code != tc.errCode {
						t.Fatalf("wrong error code: got %q want %q", ve.Code, tc.errCode)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if scope == nil {
					t.Fatal("scope is nil")
				}
				if scope.Username != tc.username {
					t.Fatalf("username: got %q want %q", scope.Username, tc.username)
				}
			}
		})
	}
}

// TestClean validates path cleaning and scope enforcement.
func TestClean(t *testing.T) {
	scope, _ := NewScope("u123", "shuki", []string{"/home/shuki", "/var/www"})

	cases := []struct {
		name      string
		path      string
		wantErr   bool
		errCode   string
		wantPath  string
	}{
		{
			name:     "absolute path in scope",
			path:     "/home/shuki/file.txt",
			wantErr:  false,
			wantPath: "/home/shuki/file.txt",
		},
		{
			name:     "path with . components",
			path:     "/home/shuki/./file.txt",
			wantErr:  false,
			wantPath: "/home/shuki/file.txt",
		},
		{
			name:    "path with .. at docroot boundary (unsafe)",
			path:    "/home/shuki/subdir/../file.txt",
			wantErr: true,
			errCode: ErrCodeTraversal,
		},
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
			errCode: ErrCodeEmpty,
		},
		{
			name:    "path with null byte",
			path:    "/home/shuki/file\x00.txt",
			wantErr: true,
			errCode: ErrCodeContainsNull,
		},
		{
			name:    "path with control character",
			path:    "/home/shuki/file\x01.txt",
			wantErr: true,
			errCode: ErrCodeContainsControl,
		},
		{
			name:    "path with .. traversal",
			path:    "/home/shuki/../../../etc/passwd",
			wantErr: true,
			errCode: ErrCodeTraversal,
		},
		{
			name:    "path outside scope",
			path:    "/etc/passwd",
			wantErr: true,
			errCode: ErrCodeNotInScope,
		},
		{
			name:    "relative path",
			path:    "home/shuki/file.txt",
			wantErr: true,
			errCode: ErrCodeNotAbsolute,
		},
		{
			name:     "exact docroot",
			path:     "/home/shuki",
			wantErr:  false,
			wantPath: "/home/shuki",
		},
		{
			name:     "nested in second docroot",
			path:     "/var/www/site/index.php",
			wantErr:  false,
			wantPath: "/var/www/site/index.php",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scope.Clean(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if ve, ok := err.(*ValidationError); ok {
					if ve.Code != tc.errCode {
						t.Fatalf("wrong error code: got %q want %q", ve.Code, tc.errCode)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.wantPath {
					t.Fatalf("path: got %q want %q", got, tc.wantPath)
				}
			}
		})
	}
}

// TestBoundaryConditions tests the word-boundary check for docroot containment.
func TestBoundaryConditions(t *testing.T) {
	scope, _ := NewScope("u123", "shuki", []string{"/home/shuki"})

	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "exact match",
			path:    "/home/shuki",
			wantErr: false,
		},
		{
			name:    "proper subdirectory",
			path:    "/home/shuki/subdir",
			wantErr: false,
		},
		{
			name:    "almost-match (no slash boundary)",
			path:    "/home/shukiforensics",
			wantErr: true, // Should not match because no / boundary
		},
		{
			name:    "sibling with same prefix",
			path:    "/home/shukix/file",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scope.Clean(tc.path)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestResolve tests symlink handling and loop detection.
func TestResolve(t *testing.T) {
	// Create temporary test environment
	tmpDir := t.TempDir()
	docroot := filepath.Join(tmpDir, "docroot")
	os.Mkdir(docroot, 0755)

	// Create a regular file
	regularFile := filepath.Join(docroot, "regular.txt")
	os.WriteFile(regularFile, []byte("content"), 0644)

	// Create a symlink to the regular file
	symFile := filepath.Join(docroot, "link.txt")
	os.Symlink(regularFile, symFile)

	// Create an external target outside docroot
	external := filepath.Join(tmpDir, "external.txt")
	os.WriteFile(external, []byte("external"), 0644)

	// Create a symlink pointing outside docroot
	escapeLinkPath := filepath.Join(docroot, "escape_link.txt")
	os.Symlink(external, escapeLinkPath)

	scope, _ := NewScope("u123", "shuki", []string{docroot})

	cases := []struct {
		name    string
		path    string
		wantErr bool
		errCode string
	}{
		{
			name:    "regular file in scope",
			path:    regularFile,
			wantErr: false,
		},
		{
			name:    "symlink to file in scope",
			path:    symFile,
			wantErr: false, // Should resolve to regular.txt which is in scope
		},
		{
			name:    "symlink escape attempt",
			path:    escapeLinkPath,
			wantErr: true,
			errCode: ErrCodeNotInScope, // Resolved path is outside docroot
		},
		{
			name:    "nonexistent path but valid scope",
			path:    filepath.Join(docroot, "nonexistent.txt"),
			wantErr: false, // EvalSymlinks allows nonexistent paths
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scope.Resolve(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if ve, ok := err.(*ValidationError); ok && tc.errCode != "" {
					if ve.Code != tc.errCode {
						t.Fatalf("wrong error code: got %q want %q", ve.Code, tc.errCode)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestOpen tests file opening with O_NOFOLLOW protection.
func TestOpen(t *testing.T) {
	tmpDir := t.TempDir()
	docroot := filepath.Join(tmpDir, "docroot")
	os.Mkdir(docroot, 0755)

	// Create a file and a symlink to it
	regularFile := filepath.Join(docroot, "regular.txt")
	os.WriteFile(regularFile, []byte("content"), 0644)

	symFile := filepath.Join(docroot, "link.txt")
	os.Symlink(regularFile, symFile)

	scope, _ := NewScope("u123", "shuki", []string{docroot})

	// Test opening regular file
	f, err := scope.Open(regularFile, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("failed to open regular file: %v", err)
	}
	f.Close()

	// Test opening symlink should fail due to O_NOFOLLOW (TOCTOU protection)
	f, err = scope.Open(symFile, os.O_RDONLY, 0)
	if err == nil {
		f.Close()
		t.Fatal("expected error opening symlink with O_NOFOLLOW, got nil")
	}

	// Test opening nonexistent file should fail
	_, err = scope.Open(filepath.Join(docroot, "nonexistent.txt"), os.O_RDONLY, 0)
	if err == nil {
		t.Fatal("expected error opening nonexistent file, got nil")
	}
}

// TestChownToUser validates chown with symlink protection.
func TestChownToUser(t *testing.T) {
	tmpDir := t.TempDir()
	docroot := filepath.Join(tmpDir, "docroot")
	os.Mkdir(docroot, 0755)

	// Create a file
	regularFile := filepath.Join(docroot, "file.txt")
	os.WriteFile(regularFile, []byte("content"), 0644)

	// Create a symlink
	symFile := filepath.Join(docroot, "link.txt")
	os.Symlink(regularFile, symFile)

	scope, _ := NewScope("u123", "shuki", []string{docroot})

	// Attempt to chown a symlink should fail
	err := scope.ChownToUser(symFile, os.Getuid(), os.Getgid())
	if err == nil {
		t.Fatal("expected error when chowning symlink, got nil")
	}
	if ve, ok := err.(*ValidationError); ok {
		if ve.Code != ErrCodeSymlinkEscape {
			t.Fatalf("wrong error code: got %q want %q", ve.Code, ErrCodeSymlinkEscape)
		}
	}

	// Attempt to chown regular file
	err = scope.ChownToUser(regularFile, os.Getuid(), os.Getgid())
	if err != nil {
		// May succeed or fail depending on permissions, but shouldn't be symlink escape error
		if ve, ok := err.(*ValidationError); ok && ve.Code == ErrCodeSymlinkEscape {
			t.Fatalf("unexpected symlink escape error: %v", err)
		}
	}
}

// TestPathTraversalVectors tests various path traversal attack vectors.
func TestPathTraversalVectors(t *testing.T) {
	scope, _ := NewScope("u123", "shuki", []string{"/home/shuki"})

	cases := []struct {
		name  string
		path  string
		want  bool // true = should succeed, false = should fail
	}{
		{
			name: "simple traversal with ..",
			path: "/home/shuki/../../etc/passwd",
			want: false,
		},
		{
			name: "unicode encoding attempt (should reject raw ..)",
			path: "/home/shuki/\x2e\x2e/etc/passwd",
			want: false,
		},
		{
			name: "double encoding %252e%252e",
			path: "/home/shuki/%252e%252e/etc/passwd",
			want: true, // URL encoding is not decoded by filepath.Clean
		},
		{
			name: "backtracking at boundary",
			path: "/home/shuki/../../../outside",
			want: false,
		},
		{
			name: "relative with ..",
			path: "shuki/../../etc/passwd",
			want: false, // Should fail on not absolute before traversal check
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scope.Clean(tc.path)
			gotSuccess := err == nil
			if gotSuccess != tc.want {
				t.Fatalf("path traversal test: got success=%v want=%v (err=%v)", gotSuccess, tc.want, err)
			}
		})
	}
}

// TestControlCharacterRejection tests rejection of various control characters.
func TestControlCharacterRejection(t *testing.T) {
	scope, _ := NewScope("u123", "shuki", []string{"/home/shuki"})

	cases := []struct {
		name  string
		path  string
		valid bool
	}{
		{
			name:  "null byte",
			path:  "/home/shuki/file\x00.txt",
			valid: false,
		},
		{
			name:  "bell character",
			path:  "/home/shuki/file\x07.txt",
			valid: false,
		},
		{
			name:  "form feed",
			path:  "/home/shuki/file\x0c.txt",
			valid: false,
		},
		{
			name:  "escape character",
			path:  "/home/shuki/file\x1b.txt",
			valid: false,
		},
		{
			name:  "normal path",
			path:  "/home/shuki/file.txt",
			valid: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scope.Clean(tc.path)
			if tc.valid && err != nil {
				t.Fatalf("expected valid path, got error: %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("expected invalid path to error, got nil")
			}
		})
	}
}

// TestLongPathRejection tests rejection of paths exceeding 4096 bytes.
func TestLongPathRejection(t *testing.T) {
	scope, _ := NewScope("u123", "shuki", []string{"/home/shuki"})

	// Create a path longer than 4096 bytes
	longPath := "/home/shuki/" + string(make([]byte, 4100))
	for i := range longPath[12:] {
		longPath = longPath[:12+i] + "a" + longPath[12+i+1:]
	}

	_, err := scope.Clean(longPath)
	if err == nil {
		t.Fatal("expected error for long path, got nil")
	}
	if ve, ok := err.(*ValidationError); ok {
		if ve.Code != ErrCodeTooLong {
			t.Fatalf("wrong error code: got %q want %q", ve.Code, ErrCodeTooLong)
		}
	}
}

// TestMultipleDocroots tests path validation across multiple docroots.
func TestMultipleDocroots(t *testing.T) {
	scope, _ := NewScope("u123", "shuki", []string{"/home/shuki", "/var/www", "/opt/app"})

	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "path in first docroot",
			path:    "/home/shuki/file.txt",
			wantErr: false,
		},
		{
			name:    "path in second docroot",
			path:    "/var/www/site/index.php",
			wantErr: false,
		},
		{
			name:    "path in third docroot",
			path:    "/opt/app/config.yaml",
			wantErr: false,
		},
		{
			name:    "path outside all docroots",
			path:    "/etc/passwd",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scope.Clean(tc.path)
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
