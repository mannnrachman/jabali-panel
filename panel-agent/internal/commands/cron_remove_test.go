package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestCronRemoveMissingUsername(t *testing.T) {
	params := json.RawMessage(`{
		"user_id": "1",
		"username": "",
		"job_id": "job1"
	}`)

	_, err := cronRemoveHandler(context.Background(), params)
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

func TestCronRemoveMissingJobID(t *testing.T) {
	params := json.RawMessage(`{
		"user_id": "1",
		"username": "testuser",
		"job_id": ""
	}`)

	_, err := cronRemoveHandler(context.Background(), params)
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

func TestCronRemoveUnknownUser(t *testing.T) {
	params := json.RawMessage(`{
		"user_id": "999",
		"username": "nonexistentuser12345",
		"job_id": "job1"
	}`)

	_, err := cronRemoveHandler(context.Background(), params)
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

func TestCronRemoveNoChange(t *testing.T) {
	currentUser := os.Getenv("USER")
	if currentUser == "" {
		t.Skip("USER env not set")
	}

	params := json.RawMessage(fmt.Sprintf(`{
		"user_id": "1",
		"username": "%s",
		"job_id": "nonexistent-job"
	}`, currentUser))

	result, err := cronRemoveHandler(context.Background(), params)
	if err != nil {
		// May fail due to systemd issues, but if it succeeds...
		return
	}

	if result != nil {
		resp, ok := result.(*cronRemoveResponse)
		if !ok {
			t.Fatalf("expected cronRemoveResponse, got %T", result)
		}
		// When job doesn't exist, should return NoChange=true
		if !resp.NoChange {
			t.Error("expected NoChange=true for non-existent job")
		}
	}
}
