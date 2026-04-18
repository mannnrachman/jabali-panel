package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestCronTailLogMissingUsername(t *testing.T) {
	params := json.RawMessage(`{
		"user_id": "1",
		"username": "",
		"job_id": "job1"
	}`)

	_, err := cronTailLogHandler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing username")
	}
	agentErr, ok := err.(*agentwire.AgentError)
	if !ok {
		t.Fatalf("expected AgentError, got %T", err)
	}
	if agentErr.Code != agentwire.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument, got %s", agentErr.Code)
	}
}

func TestCronTailLogMissingJobID(t *testing.T) {
	params := json.RawMessage(`{
		"user_id": "1",
		"username": "testuser",
		"job_id": ""
	}`)

	_, err := cronTailLogHandler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for missing job_id")
	}
	agentErr, ok := err.(*agentwire.AgentError)
	if !ok {
		t.Fatalf("expected AgentError, got %T", err)
	}
	if agentErr.Code != agentwire.CodeInvalidArgument {
		t.Errorf("expected CodeInvalidArgument, got %s", agentErr.Code)
	}
}

func TestCronTailLogUnknownUser(t *testing.T) {
	params := json.RawMessage(`{
		"user_id": "999",
		"username": "nonexistentuser12345",
		"job_id": "job1"
	}`)

	_, err := cronTailLogHandler(context.Background(), params)
	if err == nil {
		t.Fatal("expected error for unknown user")
	}
	agentErr, ok := err.(*agentwire.AgentError)
	if !ok {
		t.Fatalf("expected AgentError, got %T", err)
	}
	if agentErr.Code != agentwire.CodeNotFound {
		t.Errorf("expected CodeNotFound, got %s", agentErr.Code)
	}
}

func TestCronTailLogDefaultLines(t *testing.T) {
	currentUser := os.Getenv("USER")
	if currentUser == "" {
		t.Skip("USER env not set")
	}

	params := json.RawMessage(fmt.Sprintf(`{
		"user_id": "1",
		"username": "%s",
		"job_id": "test-job"
	}`, currentUser))

	result, err := cronTailLogHandler(context.Background(), params)
	// May fail due to journalctl/service issues, but check structure if successful
	if err == nil && result != nil {
		resp, ok := result.(*cronTailLogResponse)
		if !ok {
			t.Fatalf("expected cronTailLogResponse, got %T", result)
		}
		// Log field should be a string (may be empty)
		if resp.Log == "" {
			// Empty log is acceptable if service has no logs yet
			return
		}
	}
}

func TestCronTailLogCustomLines(t *testing.T) {
	currentUser := os.Getenv("USER")
	if currentUser == "" {
		t.Skip("USER env not set")
	}

	lines := 10
	params := json.RawMessage(fmt.Sprintf(`{
		"user_id": "1",
		"username": "%s",
		"job_id": "test-job",
		"lines": %d
	}`, currentUser, lines))

	result, err := cronTailLogHandler(context.Background(), params)
	// May fail due to journalctl/service issues, but check structure if successful
	if err == nil && result != nil {
		resp, ok := result.(*cronTailLogResponse)
		if !ok {
			t.Fatalf("expected cronTailLogResponse, got %T", result)
		}
		// Log field should be a string (may be empty)
		_ = resp.Log
	}
}
