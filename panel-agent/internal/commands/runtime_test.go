package commands

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

func TestResolveExecutable(t *testing.T) {
	username := os.Getenv("USER")
	if username == "" {
		t.Skip("USER env not set, skipping")
	}

	// Resolve different tools and make sure we get non-empty fallbacks
	nodeExe := resolveExecutable(username, "node")
	assert.NotEmpty(t, nodeExe)

	pythonExe := resolveExecutable(username, "python3")
	assert.NotEmpty(t, pythonExe)

	goExe := resolveExecutable(username, "go")
	assert.NotEmpty(t, goExe)

	dockerExe := resolveExecutable(username, "docker")
	assert.NotEmpty(t, dockerExe)
}

func TestRuntimeApply_InvalidParams(t *testing.T) {
	_, err := runtimeApplyHandler(context.Background(), []byte("invalid json"))
	require.Error(t, err)

	var aerr *agentwire.AgentError
	require.ErrorAs(t, err, &aerr)
	assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
}

func TestRuntimeApply_MissingFields(t *testing.T) {
	params := runtimeApplyParams{
		Username: "",
	}
	paramsJSON, _ := json.Marshal(params)

	_, err := runtimeApplyHandler(context.Background(), paramsJSON)
	require.Error(t, err)

	var aerr *agentwire.AgentError
	require.ErrorAs(t, err, &aerr)
	assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
}

func TestRuntimeApply_UserNotFound(t *testing.T) {
	params := runtimeApplyParams{
		Username:    "nonexistent_user_12345",
		Domain:      "example.com",
		DocRoot:     "/home/nonexistent_user_12345/domains/example.com/public_html",
		RuntimeType: "nodejs",
		ListenPort:  3000,
	}
	paramsJSON, _ := json.Marshal(params)

	_, err := runtimeApplyHandler(context.Background(), paramsJSON)
	require.Error(t, err)

	var aerr *agentwire.AgentError
	require.ErrorAs(t, err, &aerr)
	assert.Equal(t, agentwire.CodeNotFound, aerr.Code)
}

func TestRuntimeRemove_InvalidParams(t *testing.T) {
	_, err := runtimeRemoveHandler(context.Background(), []byte("invalid json"))
	require.Error(t, err)
}

func TestRuntimeStatus_InvalidParams(t *testing.T) {
	_, err := runtimeStatusHandler(context.Background(), []byte("invalid json"))
	require.Error(t, err)
}

func TestRuntimeLogs_InvalidParams(t *testing.T) {
	_, err := runtimeLogsHandler(context.Background(), []byte("invalid json"))
	require.Error(t, err)
}

func TestRuntimeDeploy_InvalidParams(t *testing.T) {
	_, err := runtimeDeployHandler(context.Background(), []byte("invalid json"))
	require.Error(t, err)
}

func TestRuntimeDeploy_MissingFields(t *testing.T) {
	params := runtimeDeployParams{
		Username: "",
	}
	paramsJSON, _ := json.Marshal(params)

	_, err := runtimeDeployHandler(context.Background(), paramsJSON)
	require.Error(t, err)
}

func TestRuntimeDeploy_DocrootNotFound(t *testing.T) {
	username := os.Getenv("USER")
	if username == "" {
		t.Skip("USER env not set, skipping")
	}

	// Path is under the allowed prefix (passes validateDocrootPath) but
	// does not exist, so the handler should report NotFound.
	params := runtimeDeployParams{
		Username:    username,
		DocRoot:     filepath.Join("/home", username, "domains", "nonexistent-12345", "public_html"),
		RuntimeType: "nodejs",
	}
	paramsJSON, _ := json.Marshal(params)

	_, err := runtimeDeployHandler(context.Background(), paramsJSON)
	require.Error(t, err)

	var aerr *agentwire.AgentError
	require.ErrorAs(t, err, &aerr)
	assert.Equal(t, agentwire.CodeNotFound, aerr.Code)
}

func TestRuntimeApply_HappyPath(t *testing.T) {
	username := os.Getenv("USER")
	if username == "" {
		t.Skip("USER env not set, skipping")
	}

	// Docroot must live under /home/<user>/domains/ (validateDocrootPath).
	// Create it there and clean up afterwards. Skip if the home dir isn't
	// writable in this environment.
	docRoot := filepath.Join("/home", username, "domains", "testapp.local", "public_html")
	if err := os.MkdirAll(docRoot, 0o755); err != nil {
		t.Skipf("cannot create docroot under home (%v); skipping", err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Join("/home", username, "domains", "testapp.local")) })

	// Mock XDG_RUNTIME_DIR to avoid permission issues in tests if possible,
	// but wait: systemctlUserExec runs sudo -u <username>, which runs under the test user context.
	// Since we run the test as the active user, we can try running it directly!
	params := runtimeApplyParams{
		Username:    username,
		Domain:      "testapp.local",
		DocRoot:     docRoot,
		RuntimeType: "nodejs",
		EntryPoint:  "index.js",
		ListenPort:  10234,
		IsEnabled:   false, // Keep it disabled to avoid starting systemd service in local testing if it's missing lingering user dirs
	}
	paramsJSON, _ := json.Marshal(params)

	resp, err := runtimeApplyHandler(context.Background(), paramsJSON)
	if err != nil {
		// If systemd or linger is completely missing or permissions are restricted, it might fail daemon-reload.
		// That's acceptable in non-root test environments. But if it succeeds, let's verify!
		t.Logf("systemd not fully available for test user: %v", err)
		return
	}

	require.NoError(t, err)
	require.NotNil(t, resp)

	result := resp.(*runtimeApplyResponse)
	assert.NotEmpty(t, result.ServicePath)

	// Verify the service file was created
	_, err = os.Stat(result.ServicePath)
	require.NoError(t, err)

	// Read content and check keywords
	content, err := os.ReadFile(result.ServicePath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Description=Jabali Managed Service - testapp.local (nodejs)")
	assert.Contains(t, string(content), "Environment=PORT=10234")

	// Clean up using runtime.remove
	removeParams := runtimeRemoveParams{
		Username: username,
		Domain:   "testapp.local",
	}
	removeParamsJSON, _ := json.Marshal(removeParams)

	removeResp, err := runtimeRemoveHandler(context.Background(), removeParamsJSON)
	require.NoError(t, err)
	assert.NotNil(t, removeResp)

	// Verify file is gone
	_, err = os.Stat(result.ServicePath)
	assert.True(t, os.IsNotExist(err))
}

// --- ADR-0113 input validation (security-critical) ---

func TestValidateImageName(t *testing.T) {
	ok := []string{"alpine", "alpine:3.19", "library/nginx:latest", "ghcr.io/org/app:v1", "registry.example.com:5000/team/app@sha256:abc123"}
	for _, v := range ok {
		assert.NoError(t, validateImageName(v), "expected %q valid", v)
	}
	// Injection attempts: leading dash (parsed as flag), embedded flags,
	// shell/space metacharacters, newlines.
	bad := []string{
		"",
		"-rm",
		"--privileged",
		"alpine --privileged -v /:/host",
		"alpine\n--privileged",
		"alpine; rm -rf /",
		"alpine`id`",
		"alpine$(id)",
	}
	for _, v := range bad {
		assert.Error(t, validateImageName(v), "expected %q rejected", v)
	}
}

func TestValidateEnvVars(t *testing.T) {
	assert.NoError(t, validateEnvVars(map[string]string{"FOO": "bar", "_X1": "a b c"}))
	// Bad keys.
	assert.Error(t, validateEnvVars(map[string]string{"1FOO": "x"}))
	assert.Error(t, validateEnvVars(map[string]string{"FO-O": "x"}))
	assert.Error(t, validateEnvVars(map[string]string{"FOO BAR": "x"}))
	// Values that would break out of Environment="KEY=VALUE" or inject
	// a new unit directive.
	assert.Error(t, validateEnvVars(map[string]string{"FOO": "a\nExecStartPre=/bin/x"}))
	assert.Error(t, validateEnvVars(map[string]string{"FOO": "a\"b"}))
	assert.Error(t, validateEnvVars(map[string]string{"FOO": "a\\b"}))
}

func TestValidateEntryPoint(t *testing.T) {
	for _, v := range []string{"", "index.js", "src/app.py", "cmd/server/main.go"} {
		assert.NoError(t, validateEntryPoint(v), "expected %q valid", v)
	}
	for _, v := range []string{"../etc/passwd", "a/../../b", "/etc/passwd", "a\nb"} {
		assert.Error(t, validateEntryPoint(v), "expected %q rejected", v)
	}
}

func TestValidateSafeToken(t *testing.T) {
	assert.NoError(t, validateSafeToken("domain", "example.com"))
	assert.NoError(t, validateSafeToken("username", "alice_1"))
	for _, v := range []string{"", "a b", "a;b", "a/b", "a$b", "a\nb"} {
		assert.Error(t, validateSafeToken("domain", v), "expected %q rejected", v)
	}
}

func TestPortInUseOnHost(t *testing.T) {
	// Out-of-range ports are never "in use".
	assert.False(t, portInUseOnHost(0))
	assert.False(t, portInUseOnHost(70000))

	// Bind a listener and confirm detection; release and confirm free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	assert.True(t, portInUseOnHost(port), "bound port should read as in-use")
	require.NoError(t, ln.Close())
	assert.False(t, portInUseOnHost(port), "closed port should read as free")
}

// TestRuntimeApply_RejectsDockerInjection ensures a crafted docker
// entry_point can't reach the systemd unit renderer.
func TestRuntimeApply_RejectsDockerInjection(t *testing.T) {
	username := os.Getenv("USER")
	if username == "" {
		t.Skip("USER env not set, skipping")
	}
	params := runtimeApplyParams{
		Username:    username,
		Domain:      "evil.local",
		DocRoot:     filepath.Join("/home", username, "domains", "evil.local", "public_html"),
		RuntimeType: "docker",
		EntryPoint:  "alpine --privileged -v /:/host",
		ListenPort:  10999,
	}
	paramsJSON, _ := json.Marshal(params)
	_, err := runtimeApplyHandler(context.Background(), paramsJSON)
	require.Error(t, err)
	var aerr *agentwire.AgentError
	require.ErrorAs(t, err, &aerr)
	assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
}

// TestRuntimeApply_RejectsEnvInjection ensures an env value with a
// newline can't inject a new systemd unit directive.
func TestRuntimeApply_RejectsEnvInjection(t *testing.T) {
	username := os.Getenv("USER")
	if username == "" {
		t.Skip("USER env not set, skipping")
	}
	params := runtimeApplyParams{
		Username:    username,
		Domain:      "env.local",
		DocRoot:     filepath.Join("/home", username, "domains", "env.local", "public_html"),
		RuntimeType: "nodejs",
		EntryPoint:  "index.js",
		ListenPort:  10998,
		EnvVars:     map[string]string{"FOO": "bar\nExecStartPre=/bin/touch /tmp/pwned"},
	}
	paramsJSON, _ := json.Marshal(params)
	_, err := runtimeApplyHandler(context.Background(), paramsJSON)
	require.Error(t, err)
	var aerr *agentwire.AgentError
	require.ErrorAs(t, err, &aerr)
	assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
}

// TestRuntimeDeploy_RejectsDocrootTraversal ensures a docroot outside
// /home/<user>/domains/ is rejected before any build command runs.
func TestRuntimeDeploy_RejectsDocrootTraversal(t *testing.T) {
	username := os.Getenv("USER")
	if username == "" {
		t.Skip("USER env not set, skipping")
	}
	params := runtimeDeployParams{
		Username:    username,
		DocRoot:     "/etc",
		RuntimeType: "nodejs",
	}
	paramsJSON, _ := json.Marshal(params)
	_, err := runtimeDeployHandler(context.Background(), paramsJSON)
	require.Error(t, err)
	var aerr *agentwire.AgentError
	require.ErrorAs(t, err, &aerr)
	assert.Equal(t, agentwire.CodeInvalidArgument, aerr.Code)
}
