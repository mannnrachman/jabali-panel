// restore_appconfigs.go — M35.8 P8. After ImportDatabases creates
// new (db_name, db_user, db_pass) triples + ImportHomeSplit rsync's
// app files into /home/<user>/domains/<dom>/public_html/, every
// WordPress/Drupal/Joomla/Magento app's config file still points
// at the SOURCE db credentials (cpanel preserved the names but the
// passwords are new). This writer scans each per-domain docroot
// for known app signatures + splices the new triple in via
// agent.files.read + files.write.
//
// Today's matchers (more to follow as we observe live tarballs):
//   wp-config.php        — WordPress define('DB_NAME', ...) lines
//   configuration.php    — Joomla public $db, $user, $password
//   sites/default/settings.php — Drupal databases array
//   app/etc/env.php      — Magento 2 'db' => ['connection' => ...]
//
// Strategy: read file content, regex-replace the value (preserving
// surrounding syntax), write back. If a single file matches more
// than one app (rare collision) we apply every matcher — they're
// scoped to their own marker strings so they don't clobber each
// other.

package cpanel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/agent"
)

// AppConfigsResult tallies per-app rewrites.
type AppConfigsResult struct {
	WordPress int      // wp-config.php files rewritten
	Joomla    int      // configuration.php files rewritten
	Drupal    int      // settings.php files rewritten
	Magento   int      // app/etc/env.php files rewritten
	Skipped   []string // human-readable reasons (file unreadable, no match, etc.)
}

// ImportAppConfigs walks every domain docroot under
// /home/<targetUsername>/domains/*/public_html/ and rewrites any
// app-config file it finds with the new (db_name, db_user, db_pass)
// from `creds`. `creds` is keyed by db_name; the rewriter picks the
// first credential that matches the file's existing DB_NAME line
// (modern cpanel migrations preserve names, so the match is a
// straight string lookup).
func ImportAppConfigs(
	ctx context.Context,
	agentCli agent.AgentInterface,
	targetUserID, targetUsername string,
	creds map[string]DBCredential,
) (*AppConfigsResult, error) {
	res := &AppConfigsResult{}
	if agentCli == nil || len(creds) == 0 {
		return res, nil
	}
	root := filepath.Join("/home", targetUsername, "domains")
	entries, err := os.ReadDir(root)
	if err != nil {
		res.Skipped = append(res.Skipped, fmt.Sprintf("appconfigs_read_root:%s:%v", root, err))
		return res, nil
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		docroot := filepath.Join(root, e.Name(), "public_html")
		// WordPress
		for _, candidate := range []string{
			filepath.Join(docroot, "wp-config.php"),
			filepath.Join(docroot, "wp", "wp-config.php"),
		} {
			if n, sk := rewriteOne(ctx, agentCli, targetUserID, targetUsername, candidate, creds, rewriteWordPress); n > 0 {
				res.WordPress += n
			} else if sk != "" {
				res.Skipped = append(res.Skipped, sk)
			}
		}
		// Joomla
		joomla := filepath.Join(docroot, "configuration.php")
		if n, sk := rewriteOne(ctx, agentCli, targetUserID, targetUsername, joomla, creds, rewriteJoomla); n > 0 {
			res.Joomla += n
		} else if sk != "" {
			res.Skipped = append(res.Skipped, sk)
		}
		// Drupal
		drupal := filepath.Join(docroot, "sites", "default", "settings.php")
		if n, sk := rewriteOne(ctx, agentCli, targetUserID, targetUsername, drupal, creds, rewriteDrupal); n > 0 {
			res.Drupal += n
		} else if sk != "" {
			res.Skipped = append(res.Skipped, sk)
		}
		// Magento 2
		magento := filepath.Join(docroot, "app", "etc", "env.php")
		if n, sk := rewriteOne(ctx, agentCli, targetUserID, targetUsername, magento, creds, rewriteMagento); n > 0 {
			res.Magento += n
		} else if sk != "" {
			res.Skipped = append(res.Skipped, sk)
		}
	}
	return res, nil
}

// rewriteOne reads `path` via agent.files.read, runs the rewriter,
// writes it back. Returns (count, skipReason). count is 0 + skip ""
// when the file doesn't exist (silent skip — most domains aren't
// every CMS). count is 1 when the rewriter mutated bytes; count is
// 0 + skip="…" on read/write error or no-op.
func rewriteOne(
	ctx context.Context,
	agentCli agent.AgentInterface,
	targetUserID, targetUsername, path string,
	creds map[string]DBCredential,
	rewriter func(text string, creds map[string]DBCredential) (string, bool),
) (int, string) {
	if _, err := os.Stat(path); err != nil {
		return 0, ""
	}
	raw, err := agentCli.Call(ctx, "files.read", map[string]any{
		"user_id":  targetUserID,
		"username": targetUsername,
		"path":     path, // absolute — agent's filesafe.Resolve rejects relative
	})
	if err != nil {
		return 0, fmt.Sprintf("appconfig_read_skip:%s:%v", path, err)
	}
	var r struct {
		Content    string `json:"content"`
		IsBinary   bool   `json:"is_binary"`
	}
	if jErr := json.Unmarshal(raw, &r); jErr != nil || r.IsBinary {
		return 0, fmt.Sprintf("appconfig_decode_skip:%s", path)
	}
	updated, changed := rewriter(r.Content, creds)
	if !changed {
		return 0, ""
	}
	if _, err := agentCli.Call(ctx, "files.write", map[string]any{
		"user_id":  targetUserID,
		"username": targetUsername,
		"path":     path,
		"content":  updated,
		"mode":     "overwrite",
	}); err != nil {
		return 0, fmt.Sprintf("appconfig_write_skip:%s:%v", path, err)
	}
	return 1, ""
}

// ---- per-app rewriters ----

var (
	wpDefineRe = regexp.MustCompile(`(?m)^\s*define\(\s*['"](DB_NAME|DB_USER|DB_PASSWORD|DB_HOST)['"]\s*,\s*['"]([^'"]*)['"]\s*\)\s*;`)
)

func rewriteWordPress(text string, creds map[string]DBCredential) (string, bool) {
	if !strings.Contains(text, "DB_NAME") {
		return text, false
	}
	// Find the source DB_NAME first so we can match it against a credential.
	var sourceDB, dst string
	for _, m := range wpDefineRe.FindAllStringSubmatch(text, -1) {
		if m[1] == "DB_NAME" {
			sourceDB = m[2]
			break
		}
	}
	creds2 := creds
	cred, ok := creds2[sourceDB]
	if !ok {
		// fallback — single-cred case: use the only entry
		if len(creds2) == 1 {
			for _, c := range creds2 {
				cred = c
				ok = true
			}
		}
	}
	if !ok {
		return text, false
	}
	dst = text
	dst = wpDefineRe.ReplaceAllStringFunc(dst, func(line string) string {
		m := wpDefineRe.FindStringSubmatch(line)
		key := m[1]
		switch key {
		case "DB_NAME":
			return fmt.Sprintf("define('DB_NAME', '%s');", phpEscape(cred.DBName))
		case "DB_USER":
			return fmt.Sprintf("define('DB_USER', '%s');", phpEscape(cred.DBUser))
		case "DB_PASSWORD":
			return fmt.Sprintf("define('DB_PASSWORD', '%s');", phpEscape(cred.Password))
		case "DB_HOST":
			return "define('DB_HOST', 'localhost');"
		}
		return line
	})
	return dst, dst != text
}

var (
	joomlaAssignRe = regexp.MustCompile(`(?m)public \$(db|user|password|host)\s*=\s*['"]([^'"]*)['"]\s*;`)
)

func rewriteJoomla(text string, creds map[string]DBCredential) (string, bool) {
	if !strings.Contains(text, "public $db") {
		return text, false
	}
	var sourceDB string
	for _, m := range joomlaAssignRe.FindAllStringSubmatch(text, -1) {
		if m[1] == "db" {
			sourceDB = m[2]
			break
		}
	}
	cred, ok := creds[sourceDB]
	if !ok && len(creds) == 1 {
		for _, c := range creds {
			cred = c
			ok = true
		}
	}
	if !ok {
		return text, false
	}
	out := joomlaAssignRe.ReplaceAllStringFunc(text, func(line string) string {
		m := joomlaAssignRe.FindStringSubmatch(line)
		switch m[1] {
		case "db":
			return fmt.Sprintf("public $db = '%s';", phpEscape(cred.DBName))
		case "user":
			return fmt.Sprintf("public $user = '%s';", phpEscape(cred.DBUser))
		case "password":
			return fmt.Sprintf("public $password = '%s';", phpEscape(cred.Password))
		case "host":
			return "public $host = 'localhost';"
		}
		return line
	})
	return out, out != text
}

var (
	drupalKVRe = regexp.MustCompile(`(?m)'(database|username|password|host)'\s*=>\s*['"]([^'"]*)['"]`)
)

func rewriteDrupal(text string, creds map[string]DBCredential) (string, bool) {
	if !strings.Contains(text, "'database'") {
		return text, false
	}
	var sourceDB string
	for _, m := range drupalKVRe.FindAllStringSubmatch(text, -1) {
		if m[1] == "database" {
			sourceDB = m[2]
			break
		}
	}
	cred, ok := creds[sourceDB]
	if !ok && len(creds) == 1 {
		for _, c := range creds {
			cred = c
			ok = true
		}
	}
	if !ok {
		return text, false
	}
	out := drupalKVRe.ReplaceAllStringFunc(text, func(line string) string {
		m := drupalKVRe.FindStringSubmatch(line)
		switch m[1] {
		case "database":
			return fmt.Sprintf("'database' => '%s'", phpEscape(cred.DBName))
		case "username":
			return fmt.Sprintf("'username' => '%s'", phpEscape(cred.DBUser))
		case "password":
			return fmt.Sprintf("'password' => '%s'", phpEscape(cred.Password))
		case "host":
			return "'host' => 'localhost'"
		}
		return line
	})
	return out, out != text
}

// Magento env.php is also PHP-array form but its dbname / username
// / password keys live under db->connection->default. Reuse the
// same key-value regex; it's lenient enough.
var (
	magentoKVRe = regexp.MustCompile(`(?m)'(dbname|username|password|host)'\s*=>\s*['"]([^'"]*)['"]`)
)

func rewriteMagento(text string, creds map[string]DBCredential) (string, bool) {
	if !strings.Contains(text, "'connection'") || !strings.Contains(text, "'dbname'") {
		return text, false
	}
	var sourceDB string
	for _, m := range magentoKVRe.FindAllStringSubmatch(text, -1) {
		if m[1] == "dbname" {
			sourceDB = m[2]
			break
		}
	}
	cred, ok := creds[sourceDB]
	if !ok && len(creds) == 1 {
		for _, c := range creds {
			cred = c
			ok = true
		}
	}
	if !ok {
		return text, false
	}
	out := magentoKVRe.ReplaceAllStringFunc(text, func(line string) string {
		m := magentoKVRe.FindStringSubmatch(line)
		switch m[1] {
		case "dbname":
			return fmt.Sprintf("'dbname' => '%s'", phpEscape(cred.DBName))
		case "username":
			return fmt.Sprintf("'username' => '%s'", phpEscape(cred.DBUser))
		case "password":
			return fmt.Sprintf("'password' => '%s'", phpEscape(cred.Password))
		case "host":
			return "'host' => 'localhost'"
		}
		return line
	})
	return out, out != text
}

// phpEscape quotes a PHP single-quoted string body: escape ' and \.
// Sufficient for db credentials which are alnum + ULID + bcrypt-safe
// chars — no embedded quotes expected, defensive only.
func phpEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}
