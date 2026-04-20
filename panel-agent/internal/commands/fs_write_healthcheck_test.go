package commands

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// currentUIDGID returns the invoking process's uid:gid as a string
// suitable for `chown`. Using numeric ids (not names) side-steps the
// common test-env failure where $USER is empty (e.g., the Gitea Actions
// runner container's `USER runner` directive exports no USER env) and
// a name-based fallback like "root:root" requires privilege the test
// user doesn't have. Numeric same-as-self is always allowed.
func currentUIDGID(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current: %v", err)
	}
	return u.Uid + ":" + u.Gid
}

func TestFSWriteHealthcheck_HappyPath(t *testing.T) {
	// Create a temporary directory and file path.
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "jabali-healthcheck.php")

	userGroup := currentUIDGID(t)

	params := map[string]string{
		"path":       filePath,
		"user_group": userGroup,
	}
	paramsJSON, _ := json.Marshal(params)

	resp, err := fsWriteHealthcheckHandler(context.Background(), paramsJSON)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	result := resp.(fsWriteHealthcheckResponse)
	if !result.Wrote {
		t.Errorf("expected Wrote=true, got false")
	}
	if result.Exists {
		t.Errorf("expected Exists=false, got true")
	}
	if result.Path != filePath {
		t.Errorf("expected Path=%s, got %s", filePath, result.Path)
	}

	// Verify file was written.
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != healthcheckPHPContent {
		t.Errorf("expected content %q, got %q", healthcheckPHPContent, string(content))
	}
}

func TestFSWriteHealthcheck_Idempotent(t *testing.T) {
	// Create a temporary file that already exists.
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "jabali-healthcheck.php")
	if err := os.WriteFile(filePath, []byte("old content"), 0644); err != nil {
		t.Fatalf("failed to create existing file: %v", err)
	}

	userGroup := currentUIDGID(t)

	params := map[string]string{
		"path":       filePath,
		"user_group": userGroup,
	}
	paramsJSON, _ := json.Marshal(params)

	resp, err := fsWriteHealthcheckHandler(context.Background(), paramsJSON)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	result := resp.(fsWriteHealthcheckResponse)
	if result.Wrote {
		t.Errorf("expected Wrote=false (already exists), got true")
	}
	if !result.Exists {
		t.Errorf("expected Exists=true, got false")
	}

	// Verify file was NOT overwritten.
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(content) != "old content" {
		t.Errorf("expected content unchanged as 'old content', got %q", string(content))
	}
}

func TestFSWriteHealthcheck_InvalidParams(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		userGrp string
	}{
		{"relative path", "relative/path.php", "user:group"},
		{"invalid user_group", "/tmp/file.php", "useronly"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]string{
				"path":       tt.path,
				"user_group": tt.userGrp,
			}
			paramsJSON, _ := json.Marshal(params)

			_, err := fsWriteHealthcheckHandler(context.Background(), paramsJSON)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}

			var ae *agentwire.AgentError
			ok := isAgentError(err, &ae)
			if !ok {
				t.Errorf("expected AgentError, got %T", err)
			}
			if ok && ae.Code == agentwire.CodeInvalidArgument {
				// Correct.
			} else if ok {
				t.Errorf("expected CodeInvalidArgument, got %v", ae.Code)
			}
		})
	}
}
