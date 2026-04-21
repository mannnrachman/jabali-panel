package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// touchFile creates a file at path with mtime set to now - ageSec.
func touchFile(t *testing.T, path string, ageSec int) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	mtime := time.Now().Add(-time.Duration(ageSec) * time.Second)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func TestReapSSOFiles_EmptyInput(t *testing.T) {
	resp, err := ReapSSOFiles(context.Background(), reapSSOFilesReq{})
	if err != nil {
		t.Fatalf("ReapSSOFiles: %v", err)
	}
	if resp.DeletedCount != 0 || resp.ScannedCount != 0 {
		t.Errorf("expected zero counts, got %+v", resp)
	}
}

func TestReapSSOFiles_DeletesStaleSkipsForeshipsLookalikes(t *testing.T) {
	dir := t.TempDir()

	// Stale: matches strict regex AND older than TTL
	stale := filepath.Join(dir, "jabali-sso-"+validNonce+".php")
	touchFile(t, stale, ssoTTLSeconds+10)

	// Fresh: matches strict regex but NOT old enough
	fresh := filepath.Join(dir, "jabali-sso-"+strings.Repeat("B", 43)+".php")
	touchFile(t, fresh, 1)

	// Lookalike with wrong nonce length (42 chars)
	short := filepath.Join(dir, "jabali-sso-"+strings.Repeat("C", 42)+".php")
	touchFile(t, short, ssoTTLSeconds+10)

	// Lookalike with prefix but invalid char (.)
	dotted := filepath.Join(dir, "jabali-sso-evil.php")
	touchFile(t, dotted, ssoTTLSeconds+10)

	// Unrelated file
	unrelated := filepath.Join(dir, "index.php")
	touchFile(t, unrelated, ssoTTLSeconds+100)

	resp, err := ReapSSOFiles(context.Background(), reapSSOFilesReq{
		InstallPaths: []string{dir},
	})
	if err != nil {
		t.Fatalf("ReapSSOFiles: %v", err)
	}

	// Stale file should be gone
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file should be deleted, but stat returned %v", err)
	}
	// Fresh file should remain
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh file should remain, got %v", err)
	}
	// Short lookalike should remain (regex doesn't match)
	if _, err := os.Stat(short); err != nil {
		t.Errorf("short-name lookalike should remain (regex miss), got %v", err)
	}
	// Dotted lookalike should remain (regex doesn't match)
	if _, err := os.Stat(dotted); err != nil {
		t.Errorf("'jabali-sso-evil.php' should remain (regex miss), got %v", err)
	}
	// Unrelated file untouched
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("index.php should remain untouched, got %v", err)
	}

	// Counts: scanned = 2 (the two regex-matching jabali-sso-<43>.php files), deleted = 1
	if resp.ScannedCount != 2 {
		t.Errorf("ScannedCount = %d, want 2", resp.ScannedCount)
	}
	if resp.DeletedCount != 1 {
		t.Errorf("DeletedCount = %d, want 1", resp.DeletedCount)
	}
}

func TestReapSSOFiles_NonexistentDirSkipped(t *testing.T) {
	resp, err := ReapSSOFiles(context.Background(), reapSSOFilesReq{
		InstallPaths: []string{"/nonexistent/path/zzz12345"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.DeletedCount != 0 || resp.ScannedCount != 0 {
		t.Errorf("expected zero counts, got %+v", resp)
	}
}

func TestReapSSOFiles_NonAbsoluteSkipped(t *testing.T) {
	resp, err := ReapSSOFiles(context.Background(), reapSSOFilesReq{
		InstallPaths: []string{"relative/path"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if resp.DeletedCount != 0 || resp.ScannedCount != 0 {
		t.Errorf("expected zero counts for non-absolute path, got %+v", resp)
	}
}

func TestReapSSOFiles_MultiplePaths(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	touchFile(t, filepath.Join(dir1, "jabali-sso-"+validNonce+".php"), ssoTTLSeconds+10)
	touchFile(t, filepath.Join(dir2, "jabali-sso-"+strings.Repeat("D", 43)+".php"), ssoTTLSeconds+10)

	resp, err := ReapSSOFiles(context.Background(), reapSSOFilesReq{
		InstallPaths: []string{dir1, dir2},
	})
	if err != nil {
		t.Fatalf("ReapSSOFiles: %v", err)
	}
	if resp.DeletedCount != 2 || resp.ScannedCount != 2 {
		t.Errorf("expected 2 deleted + 2 scanned across 2 dirs, got %+v", resp)
	}
}
