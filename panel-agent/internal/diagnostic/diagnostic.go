package diagnostic

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"filippo.io/age"
)

// Report is the wire-shape returned by system.diagnostic_report.
type Report struct {
	CiphertextB64   string `json:"ciphertext_b64"`
	ByteCount       int    `json:"byte_count"`
	GeneratedAt     string `json:"generated_at"`
	RedactionCount  int    `json:"redaction_count"`
	FileCount       int    `json:"file_count"`
}

// servicesToCollect is the fixed list of systemd units we journal-tail
// for every report. Adding a service here = automatic next-run pickup.
var servicesToCollect = []string{
	"jabali-panel.service",
	"jabali-agent.service",
	"jabali-stalwart.service",
	"jabali-webmail.service",
	"jabali-kratos.service",
	"pdns.service",
	"pdns-recursor.service",
	"mariadb.service",
	"redis-server.service",
	"nginx.service",
}

type collectedFile struct {
	Name string
	Body []byte
}

// Collect runs every host-info command and returns a list of named blobs.
// Errors are recorded inline (one error file per failure) instead of
// aborting — a failed `iptables -L` shouldn't kill the whole report.
func Collect(ctx context.Context) []collectedFile {
	files := []collectedFile{
		{"00-uname.txt", runOrErr(ctx, "uname", "-a")},
		{"01-os-release.txt", catFileOrErr("/etc/os-release")},
		{"02-uptime.txt", runOrErr(ctx, "uptime")},
		{"03-free.txt", runOrErr(ctx, "free", "-h")},
		{"04-df.txt", runOrErr(ctx, "df", "-h")},
		{"05-git-head.txt", runOrErr(ctx, "sudo", "-u", "jabali", "git", "-C", "/opt/jabali-panel", "rev-parse", "HEAD")},
		{"06-git-status.txt", runOrErr(ctx, "sudo", "-u", "jabali", "git", "-C", "/opt/jabali-panel", "status", "--porcelain")},
		{"07-ss-tnlp.txt", runOrErr(ctx, "ss", "-tnlp")},
		{"08-iptables-input.txt", runOrErr(ctx, "iptables", "-L", "INPUT", "-n")},
		{"09-dpkg-list.txt", runOrErr(ctx, "dpkg-query", "-W", "-f=${Package} ${Version}\n")},
	}
	for _, svc := range servicesToCollect {
		base := strings.TrimSuffix(svc, ".service")
		files = append(files,
			collectedFile{
				Name: fmt.Sprintf("svc/%s.is-active.txt", base),
				Body: runOrErr(ctx, "systemctl", "is-active", svc),
			},
			collectedFile{
				Name: fmt.Sprintf("svc/%s.status.txt", base),
				Body: runOrErr(ctx, "systemctl", "status", svc, "--no-pager", "-n", "0"),
			},
			collectedFile{
				Name: fmt.Sprintf("svc/%s.journal.txt", base),
				Body: runOrErr(ctx, "journalctl", "-u", svc, "-n", "200", "--no-pager"),
			},
		)
	}
	return files
}

func runOrErr(ctx context.Context, name string, args ...string) []byte {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return []byte(fmt.Sprintf("ERROR running %s %s: %v\n--- partial output ---\n%s",
			name, strings.Join(args, " "), err, string(out)))
	}
	return out
}

func catFileOrErr(path string) []byte {
	out, err := exec.Command("cat", path).Output()
	if err != nil {
		return []byte(fmt.Sprintf("ERROR reading %s: %v", path, err))
	}
	return out
}

// Encrypt redacts every collected file, packs the redacted set as a tar
// archive in memory, then encrypts the whole thing to the given age
// recipient. Returns the wire-ready Report. Caller controls the recipient
// so tests can swap in a known keypair.
func Encrypt(files []collectedFile, recipient string) (Report, error) {
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return Report{}, fmt.Errorf("parse recipient: %w", err)
	}

	totalRedactions := 0
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, f := range files {
		body, n := Redact(f.Body)
		totalRedactions += n
		hdr := &tar.Header{
			Name:    f.Name,
			Size:    int64(len(body)),
			Mode:    0o644,
			ModTime: time.Now().UTC(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return Report{}, fmt.Errorf("tar header %s: %w", f.Name, err)
		}
		if _, err := tw.Write(body); err != nil {
			return Report{}, fmt.Errorf("tar body %s: %w", f.Name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return Report{}, fmt.Errorf("tar close: %w", err)
	}

	var ciph bytes.Buffer
	w, err := age.Encrypt(&ciph, r)
	if err != nil {
		return Report{}, fmt.Errorf("age encrypt init: %w", err)
	}
	if _, err := io.Copy(w, &tarBuf); err != nil {
		return Report{}, fmt.Errorf("age write: %w", err)
	}
	if err := w.Close(); err != nil {
		return Report{}, fmt.Errorf("age close: %w", err)
	}

	return Report{
		CiphertextB64:  base64.StdEncoding.EncodeToString(ciph.Bytes()),
		ByteCount:      ciph.Len(),
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		RedactionCount: totalRedactions,
		FileCount:      len(files),
	}, nil
}
