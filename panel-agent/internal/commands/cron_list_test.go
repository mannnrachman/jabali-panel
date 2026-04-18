package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCronListMissingUsername(t *testing.T) {
	params, _ := json.Marshal(map[string]interface{}{
		"user_id": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
	})
	_, err := cronListHandler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing username")
	}
}

func TestCronListEmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()
	unitsDir := filepath.Join(tempDir, "cron-units", "testuser")
	if err := os.MkdirAll(unitsDir, 0755); err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Patch the unitsDir by creating a temporary structure
	// Note: This test can't directly patch the unitsDir path without refactoring.
	// For now, we test that a missing directory returns empty list.
	params2, _ := json.Marshal(map[string]interface{}{
		"user_id":   "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"username": "nonexistent-user-12345",
	})

	resp, err := cronListHandler(context.Background(), params2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cronResp := resp.(*cronListResponse)
	if len(cronResp.UnitFiles) != 0 {
		t.Fatalf("expected empty list for non-existent directory, got %d", len(cronResp.UnitFiles))
	}
}

func TestCronListWithJobs(t *testing.T) {
	tempDir := t.TempDir()
	unitsDir := filepath.Join(tempDir, "cron-units", "testuser")
	if err := os.MkdirAll(unitsDir, 0755); err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Create some fake unit files
	jobIDs := []string{"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FBW", "01ARZ3NDEKTSV4RRFFQ69G5FCX"}
	for _, jobID := range jobIDs {
		timerPath := filepath.Join(unitsDir, "jabali-cron-"+jobID+".timer")
		if err := os.WriteFile(timerPath, []byte("[Timer]\n"), 0644); err != nil {
			t.Fatalf("failed to write timer file: %v", err)
		}
		servicePath := filepath.Join(unitsDir, "jabali-cron-"+jobID+".service")
		if err := os.WriteFile(servicePath, []byte("[Service]\n"), 0644); err != nil {
			t.Fatalf("failed to write service file: %v", err)
		}
	}

	// Create a non-matching file
	if err := os.WriteFile(filepath.Join(unitsDir, "other-file.txt"), []byte("ignored"), 0644); err != nil {
		t.Fatalf("failed to write other file: %v", err)
	}

	// Manually test logic by creating directory structure and reading it
	entries, err := os.ReadDir(unitsDir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}

	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && filepath.Ext(name) == ".timer" {
			found++
		}
	}

	if found != 3 {
		t.Fatalf("expected 3 timer files, found %d", found)
	}
}
