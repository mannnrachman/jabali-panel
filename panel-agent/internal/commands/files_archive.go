package commands

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/internal/filesafe"
)

// files.archive — build a tar.gz of the given scoped paths and write it
// to /tmp so panel-api (same host, jabali user) can stream it out as a
// download. The archive is chowned to jabali:jabali + mode 0600 so only
// the panel process can read it; panel-api unlinks after streaming.
//
// Paths are resolved through the filesafe scope — a client cannot ask
// for /etc/passwd via the archive endpoint any more than via read().

const (
	// Panel-api's service user + a conservative cap matching the
	// upload cap — consistency with the rest of the files.* surface
	// matters more than eking out extra GB here.
	archiveOwnerUser  = "jabali"
	archiveOwnerGroup = "jabali"
	maxArchiveBytes   = int64(500 * 1024 * 1024)
)

type filesArchiveParams struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	Paths    []string `json:"paths"`
}

type filesArchiveResponse struct {
	ArchivePath string `json:"archive_path"`
	Size        int64  `json:"size"`
}

func filesArchiveHandler(ctx context.Context, params json.RawMessage) (any, error) {
	var p filesArchiveParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to parse params: %v", err),
		}
	}
	if p.Username == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "username required"}
	}
	if len(p.Paths) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "at least one path required"}
	}

	homeDir := fmt.Sprintf("/home/%s", p.Username)
	scope, err := filesafe.NewScope(p.UserID, p.Username, []string{homeDir})
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInvalidArgument,
			Message: fmt.Sprintf("failed to create scope: %v", err),
		}
	}

	// Resolve every path up-front so a bad input fails before we
	// create any scratch file.
	resolved := make([]string, 0, len(p.Paths))
	for _, raw := range p.Paths {
		r, err := scope.Resolve(raw)
		if err != nil {
			return nil, &agentwire.AgentError{
				Code:    agentwire.CodeInvalidArgument,
				Message: fmt.Sprintf("path validation failed for %q: %v", raw, err),
			}
		}
		resolved = append(resolved, r)
	}

	// Random suffix avoids collisions if two archive requests fire
	// in the same microsecond; 8 bytes hex is plenty.
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("rand: %v", err),
		}
	}
	out := filepath.Join("/tmp", fmt.Sprintf("jabali-archive-%s.tar.gz", hex.EncodeToString(rnd[:])))

	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("create archive: %v", err),
		}
	}
	// If anything below fails we don't want an orphan scratch file.
	cleanup := func() { _ = os.Remove(out) }

	// Cap the underlying file writer so a runaway archive can't fill
	// /tmp — panel-api runs as a system service and we don't want to
	// be the reason the host goes read-only.
	cw := &capWriter{w: f, limit: maxArchiveBytes}
	gz := gzip.NewWriter(cw)
	tw := tar.NewWriter(gz)

	for _, path := range resolved {
		if err := addToTar(tw, path, filepath.Dir(path)); err != nil {
			tw.Close()
			gz.Close()
			f.Close()
			cleanup()
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		f.Close()
		cleanup()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("tar close: %v", err)}
	}
	if err := gz.Close(); err != nil {
		f.Close()
		cleanup()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("gzip close: %v", err)}
	}
	if err := f.Close(); err != nil {
		cleanup()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("close: %v", err)}
	}

	info, err := os.Stat(out)
	if err != nil {
		cleanup()
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("stat: %v", err)}
	}

	// Chown to the panel-api user so it can read+unlink. Best effort:
	// if the host isn't set up with the expected user (e.g. tests),
	// we leave the file root-owned and the caller will 500.
	if err := chownToPanelUser(out); err != nil {
		// Soft warning — the file exists and the API can still try to
		// read it under its own CAP_DAC_OVERRIDE (it has none), but
		// we surface the error so operators see the misconfig.
		cleanup()
		return nil, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: fmt.Sprintf("chown archive: %v", err),
		}
	}

	return &filesArchiveResponse{
		ArchivePath: out,
		Size:        info.Size(),
	}, nil
}

// addToTar walks `src` recursively and writes every entry into `tw`,
// with paths rebased to be relative to `baseDir`. Symlinks are stored
// as-is (tar's Typeflag=TypeSymlink) — we don't chase them.
func addToTar(tw *tar.Writer, src, baseDir string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return &agentwire.AgentError{
				Code:    agentwire.CodeInternal,
				Message: fmt.Sprintf("walk %q: %v", p, walkErr),
			}
		}
		// Prefer Lstat so we see the symlink, not its target.
		li, err := os.Lstat(p)
		if err != nil {
			return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("lstat %q: %v", p, err)}
		}

		var link string
		if li.Mode()&os.ModeSymlink != 0 {
			link, _ = os.Readlink(p)
		}
		hdr, err := tar.FileInfoHeader(li, link)
		if err != nil {
			return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("tar header %q: %v", p, err)}
		}
		rel, err := filepath.Rel(baseDir, p)
		if err != nil {
			return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("rel %q: %v", p, err)}
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("write header %q: %v", p, err)}
		}
		// Only regular files carry content; dirs, symlinks, devices are
		// metadata-only in the header and must skip the body.
		if !li.Mode().IsRegular() {
			return nil
		}
		rf, err := os.Open(p)
		if err != nil {
			return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("open %q: %v", p, err)}
		}
		defer rf.Close()
		if _, err := io.Copy(tw, rf); err != nil {
			return &agentwire.AgentError{Code: agentwire.CodeInternal, Message: fmt.Sprintf("copy %q: %v", p, err)}
		}
		return nil
	})
}

// capWriter caps a writer at `limit` total bytes. Used defensively to
// refuse runaway archives — panel-api runs as a system service and a
// 50 GB tarball could fill /tmp.
type capWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.limit {
		return 0, fmt.Errorf("archive size exceeds %d bytes", c.limit)
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// chownToHostingUser sets ownership to <username>:www-data, matching
// the M9.5 homedir ownership contract. Used by files.ingest after
// moving a chunked-upload scratch file into the user's scope so it's
// accessible both to the user (for their own FPM) and to nginx (via
// the www-data group).
func chownToHostingUser(path, username string) error {
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("lookup %s: %w", username, err)
	}
	g, err := user.LookupGroup("www-data")
	if err != nil {
		return fmt.Errorf("lookup group www-data: %w", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("parse gid: %w", err)
	}
	return os.Chown(path, uid, gid)
}

// chownToPanelUser looks up jabali:jabali via name and chowns the
// file. The archive must be readable by panel-api (which runs as the
// jabali service user); leaving it root-owned would force the API to
// either fall back to a CAP_DAC_* check it doesn't have or read via
// a bounce through the agent — both uglier.
func chownToPanelUser(path string) error {
	u, err := user.Lookup(archiveOwnerUser)
	if err != nil {
		return fmt.Errorf("lookup %s: %w", archiveOwnerUser, err)
	}
	g, err := user.LookupGroup(archiveOwnerGroup)
	if err != nil {
		return fmt.Errorf("lookup group %s: %w", archiveOwnerGroup, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("parse gid: %w", err)
	}
	return os.Chown(path, uid, gid)
}

func init() {
	Default.Register("files.archive", filesArchiveHandler)
}
