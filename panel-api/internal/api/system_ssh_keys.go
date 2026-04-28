// M30.1 follow-up — admin ssh-key listing endpoint (ADR-0078).
//
// The admin needs to pick which /root/.ssh/ key the SFTP destination
// uses, or generate a new one. Key material never leaves the server;
// the API returns paths + public-key bodies (so the operator can copy
// the pubkey into the remote's authorized_keys).
package api

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/middleware"
)

// SSHKeysDir is the directory we scan for SFTP-backup keys. /root/.ssh
// is the documented location restic + ssh consult by default.
const SSHKeysDir = "/root/.ssh"

func RegisterSystemSSHKeysRoutes(rg *gin.RouterGroup) {
	h := &systemSSHKeysHandler{}
	admin := rg.Group("/admin", middleware.RequireAdmin())
	admin.GET("/system/ssh-keys", h.list)
	admin.POST("/system/ssh-keys", h.generate)
}

type systemSSHKeysHandler struct{}

type sshKeyEntry struct {
	Name        string `json:"name"`         // e.g. "id_ed25519"
	Path        string `json:"path"`         // /root/.ssh/id_ed25519
	PubkeyPath  string `json:"pubkey_path"`  // /root/.ssh/id_ed25519.pub
	Pubkey      string `json:"pubkey"`       // contents of pubkey_path, single line
	HasPassphrase bool `json:"has_passphrase"` // detected via `ssh-keygen -y -P ""`
}

// list returns every private/public key pair under SSHKeysDir whose
// private file matches `id_*` (excluding `.pub`). Keys with passphrases
// are flagged so the UI can warn the operator that BatchMode=yes auth
// will fail without ssh-agent.
func (h *systemSSHKeysHandler) list(c *gin.Context) {
	entries, err := os.ReadDir(SSHKeysDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.JSON(http.StatusOK, gin.H{"data": []sshKeyEntry{}, "total": 0})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "readdir_failed", "detail": err.Error()})
		return
	}
	out := []sshKeyEntry{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "id_") || strings.HasSuffix(name, ".pub") {
			continue
		}
		priv := filepath.Join(SSHKeysDir, name)
		pub := priv + ".pub"
		entry := sshKeyEntry{Name: name, Path: priv, PubkeyPath: pub}
		if body, err := os.ReadFile(pub); err == nil {
			entry.Pubkey = strings.TrimSpace(string(body))
		}
		entry.HasPassphrase = sshKeyHasPassphrase(priv)
		out = append(out, entry)
	}
	c.JSON(http.StatusOK, gin.H{"data": out, "total": len(out)})
}

// sshKeyHasPassphrase tries to derive the public key with an empty
// passphrase. ssh-keygen exits non-zero if the key is encrypted.
// Errors other than "wrong passphrase" (e.g. file unreadable) also
// return true so the UI errs on the side of "warn the operator".
func sshKeyHasPassphrase(privPath string) bool {
	cmd := exec.Command("ssh-keygen", "-y", "-P", "", "-f", privPath) //nolint:gosec // path comes from filesystem walk
	if err := cmd.Run(); err != nil {
		return true
	}
	return false
}

type generateKeyRequest struct {
	Name string `json:"name" binding:"required"` // basename only; e.g. "id_jabali_offsite"
	Type string `json:"type"`                    // ed25519 (default) | rsa
}

// generate runs `ssh-keygen -t <type> -f <path> -N "" -C jabali-backup`.
// The new key is written under SSHKeysDir; passphrase empty so
// BatchMode=yes works. Operator must paste the returned pubkey into
// the remote's authorized_keys.
func (h *systemSSHKeysHandler) generate(c *gin.Context) {
	var req generateKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_body", "detail": err.Error()})
		return
	}
	name := strings.TrimSpace(req.Name)
	if !validKeyName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_name",
			"detail": "name must match id_[A-Za-z0-9_]+ and contain no path separators"})
		return
	}
	keyType := req.Type
	if keyType == "" {
		keyType = "ed25519"
	}
	if keyType != "ed25519" && keyType != "rsa" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid_type"})
		return
	}
	if err := os.MkdirAll(SSHKeysDir, 0o700); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "mkdir_failed"})
		return
	}
	priv := filepath.Join(SSHKeysDir, name)
	if _, err := os.Stat(priv); err == nil {
		c.JSON(http.StatusConflict, gin.H{"status": "error", "error": "key_exists",
			"detail": "rename or delete the existing key first"})
		return
	}
	args := []string{"-t", keyType, "-f", priv, "-N", "", "-C", "jabali-backup"}
	if keyType == "rsa" {
		args = append(args, "-b", "4096")
	}
	cmd := exec.Command("ssh-keygen", args...) //nolint:gosec // args constrained above
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "error": "ssh_keygen_failed",
			"detail": err.Error(), "output": strings.TrimSpace(string(out))})
		return
	}
	pub, _ := os.ReadFile(priv + ".pub")
	c.JSON(http.StatusCreated, gin.H{
		"status":      "ok",
		"name":        name,
		"path":        priv,
		"pubkey_path": priv + ".pub",
		"pubkey":      strings.TrimSpace(string(pub)),
	})
}

func validKeyName(name string) bool {
	if !strings.HasPrefix(name, "id_") {
		return false
	}
	if strings.ContainsAny(name, "/\\.") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}
