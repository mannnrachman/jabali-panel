package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSmoke_NodePythonGoViaNginx(t *testing.T) {
	if os.Getenv("JABALI_RUNTIME_SMOKE") != "1" {
		t.Skip("set JABALI_RUNTIME_SMOKE=1 to run local runtime smoke tests")
	}

	username := os.Getenv("USER")
	if username == "" {
		t.Fatal("USER env not set")
	}
	if _, err := exec.LookPath("nginx"); err != nil {
		t.Fatalf("nginx not available: %v", err)
	}

	tests := []struct {
		name       string
		runtime    string
		entryPoint string
		wantBody   string
		files      map[string]string
	}{
		{
			name:       "nodejs",
			runtime:    "nodejs",
			entryPoint: "index.js",
			wantBody:   "node smoke ok",
			files: map[string]string{
				"package.json": `{"name":"jabali-smoke-node","version":"1.0.0","private":true}`,
				"index.js": `const http = require('http');
const port = Number(process.env.PORT || 3000);
http.createServer((req, res) => {
  res.writeHead(200, {'Content-Type': 'text/plain'});
  res.end('node smoke ok');
}).listen(port, '127.0.0.1');
`,
			},
		},
		{
			name:       "python",
			runtime:    "python",
			entryPoint: "app.py",
			wantBody:   "python smoke ok",
			files: map[string]string{
				"requirements.txt": "",
				"app.py": `import http.server, os, socketserver
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'python smoke ok'
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, fmt, *args):
        return
port = int(os.environ.get('PORT', '3000'))
with socketserver.TCPServer(('127.0.0.1', port), Handler) as httpd:
    httpd.serve_forever()
`,
			},
		},
		{
			name:       "go",
			runtime:    "go",
			entryPoint: "main.go",
			wantBody:   "go smoke ok",
			files: map[string]string{
				"main.go": `package main
import (
  "fmt"
  "net/http"
  "os"
)
func main() {
  port := os.Getenv("PORT")
  if port == "" { port = "3000" }
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    _, _ = fmt.Fprint(w, "go smoke ok")
  })
  _ = http.ListenAndServe("127.0.0.1:"+port, nil)
}
`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			domain := "smoke-" + tc.name + ".local"
			baseDir := filepath.Join("/home", username, "domains", domain)
			docRoot := filepath.Join(baseDir, "public_html")
			if err := os.MkdirAll(docRoot, 0o755); err != nil {
				t.Fatalf("mkdir docroot: %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(baseDir) })

			for rel, body := range tc.files {
				path := filepath.Join(docRoot, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir file dir %s: %v", rel, err)
				}
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatalf("write %s: %v", rel, err)
				}
			}

			deployResp, err := runtimeDeployHandler(ctx, mustJSONSmoke(t, runtimeDeployParams{
				Username:    username,
				DocRoot:     docRoot,
				RuntimeType: tc.runtime,
				EntryPoint:  tc.entryPoint,
			}))
			if err != nil {
				t.Fatalf("deploy %s failed: %v", tc.runtime, err)
			}
			if deployResp == nil {
				t.Fatalf("deploy %s returned nil response", tc.runtime)
			}

			listenPort := freePort(t)
			applyResp, err := runtimeApplyHandler(ctx, mustJSONSmoke(t, runtimeApplyParams{
				Username:    username,
				Domain:      domain,
				DocRoot:     docRoot,
				RuntimeType: tc.runtime,
				EntryPoint:  tc.entryPoint,
				ListenPort:  listenPort,
				IsEnabled:   true,
			}))
			if err != nil {
				t.Fatalf("apply %s failed: %v", tc.runtime, err)
			}
			if applyResp == nil {
				t.Fatalf("apply %s returned nil response", tc.runtime)
			}
			t.Cleanup(func() {
				_, _ = runtimeRemoveHandler(context.Background(), mustJSONSmoke(t, runtimeRemoveParams{
					Username: username,
					Domain:   domain,
				}))
			})

			want := tc.wantBody
			waitHTTPContains(t, fmt.Sprintf("http://127.0.0.1:%d/", listenPort), want)

			nginxPort := freePort(t)
			nginxDir := filepath.Join(os.TempDir(), fmt.Sprintf("jabali-nginx-%s-%d", tc.name, time.Now().UnixNano()))
			if err := os.MkdirAll(nginxDir, 0o755); err != nil {
				t.Fatalf("mkdir nginx dir: %v", err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(nginxDir) })

			conf := fmt.Sprintf(`worker_processes  1;
pid %s/nginx.pid;
error_log %s/error.log info;
events { worker_connections 128; }
http {
  access_log off;
  server {
    listen 127.0.0.1:%d;
    location / {
      proxy_pass http://127.0.0.1:%d;
      proxy_http_version 1.1;
      proxy_set_header Host $host;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto $scheme;
    }
  }
}
`, nginxDir, nginxDir, nginxPort, listenPort)
			confPath := filepath.Join(nginxDir, "nginx.conf")
			if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
				t.Fatalf("write nginx conf: %v", err)
			}

			testCmd := exec.CommandContext(ctx, "nginx", "-t", "-p", nginxDir, "-c", confPath)
			if out, err := testCmd.CombinedOutput(); err != nil {
				t.Fatalf("nginx -t failed: %v\n%s", err, string(out))
			}

			startCmd := exec.CommandContext(ctx, "nginx", "-p", nginxDir, "-c", confPath)
			if out, err := startCmd.CombinedOutput(); err != nil {
				t.Fatalf("nginx start failed: %v\n%s", err, string(out))
			}
			t.Cleanup(func() {
				stopCmd := exec.CommandContext(context.Background(), "nginx", "-s", "quit", "-p", nginxDir, "-c", confPath)
				_, _ = stopCmd.CombinedOutput()
			})

			waitHTTPContains(t, fmt.Sprintf("http://127.0.0.1:%d/", nginxPort), want)
		})
	}
}

func TestRuntimeSmoke_DockerServiceDefinition(t *testing.T) {
	if os.Getenv("JABALI_RUNTIME_SMOKE") != "1" {
		t.Skip("set JABALI_RUNTIME_SMOKE=1 to run local runtime smoke tests")
	}
	username := os.Getenv("USER")
	if username == "" {
		t.Fatal("USER env not set")
	}
	if !dockerAccessible(username) {
		t.Skip("docker daemon not accessible for current user; real docker smoke cannot run in this host session")
	}
	if _, err := exec.LookPath("nginx"); err != nil {
		t.Fatalf("nginx not available: %v", err)
	}

	ctx := context.Background()
	domain := "smoke-docker.local"
	baseDir := filepath.Join("/home", username, "domains", domain)
	docRoot := filepath.Join(baseDir, "public_html")
	if err := os.MkdirAll(docRoot, 0o755); err != nil {
		t.Fatalf("mkdir docroot: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(baseDir) })

	dockerfile := `FROM python:3.12-alpine
WORKDIR /app
COPY app.py /app/app.py
EXPOSE 8080
CMD ["python", "/app/app.py"]
`
	appPy := `import http.server, socketserver
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = b'docker smoke ok'
        self.send_response(200)
        self.send_header('Content-Type', 'text/plain')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, fmt, *args):
        return
with socketserver.TCPServer(('0.0.0.0', 8080), Handler) as httpd:
    httpd.serve_forever()
`
	for rel, body := range map[string]string{"Dockerfile": dockerfile, "app.py": appPy} {
		path := filepath.Join(docRoot, rel)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	entry := "jabali-smoke-docker:latest"
	if _, err := runtimeDeployHandler(ctx, mustJSONSmoke(t, runtimeDeployParams{
		Username:    username,
		DocRoot:     docRoot,
		RuntimeType: "docker",
		EntryPoint:  entry,
	})); err != nil {
		t.Fatalf("deploy docker failed: %v", err)
	}

	listenPort := freePort(t)
	if _, err := runtimeApplyHandler(ctx, mustJSONSmoke(t, runtimeApplyParams{
		Username:    username,
		Domain:      domain,
		DocRoot:     docRoot,
		RuntimeType: "docker",
		EntryPoint:  entry,
		ListenPort:  listenPort,
		EnvVars:     map[string]string{"CONTAINER_PORT": "8080"},
		IsEnabled:   true,
	})); err != nil {
		t.Fatalf("apply docker failed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = runtimeRemoveHandler(context.Background(), mustJSONSmoke(t, runtimeRemoveParams{Username: username, Domain: domain}))
	})

	waitHTTPContains(t, fmt.Sprintf("http://127.0.0.1:%d/", listenPort), "docker smoke ok")

	nginxPort := freePort(t)
	nginxDir := filepath.Join(os.TempDir(), fmt.Sprintf("jabali-nginx-docker-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(nginxDir, 0o755); err != nil {
		t.Fatalf("mkdir nginx dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(nginxDir) })
	conf := fmt.Sprintf(`worker_processes  1;
pid %s/nginx.pid;
error_log %s/error.log info;
events { worker_connections 128; }
http {
  access_log off;
  server {
    listen 127.0.0.1:%d;
    location / {
      proxy_pass http://127.0.0.1:%d;
      proxy_http_version 1.1;
      proxy_set_header Host $host;
    }
  }
}
`, nginxDir, nginxDir, nginxPort, listenPort)
	confPath := filepath.Join(nginxDir, "nginx.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
		t.Fatalf("write nginx conf: %v", err)
	}
	if out, err := exec.CommandContext(ctx, "nginx", "-t", "-p", nginxDir, "-c", confPath).CombinedOutput(); err != nil {
		t.Fatalf("nginx -t failed: %v\n%s", err, string(out))
	}
	if out, err := exec.CommandContext(ctx, "nginx", "-p", nginxDir, "-c", confPath).CombinedOutput(); err != nil {
		t.Fatalf("nginx start failed: %v\n%s", err, string(out))
	}
	t.Cleanup(func() {
		_, _ = exec.CommandContext(context.Background(), "nginx", "-s", "quit", "-p", nginxDir, "-c", confPath).CombinedOutput()
	})
	waitHTTPContains(t, fmt.Sprintf("http://127.0.0.1:%d/", nginxPort), "docker smoke ok")
}

func dockerAccessible(username string) bool {
	cmd := exec.Command("sudo", "-u", username, "docker", "info", "--format", "{{.ServerVersion}}")
	return cmd.Run() == nil
}

func mustJSONSmoke(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}
	return b
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitHTTPContains(t *testing.T, url, want string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(25 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK && strings.Contains(string(body), want) {
				return
			}
			if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
			}
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s to contain %q: last error: %v", url, want, lastErr)
}
