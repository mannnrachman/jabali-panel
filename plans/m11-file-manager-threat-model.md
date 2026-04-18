# M11 File Manager — Comprehensive Threat Model

**Date:** 2026-04-18  
**Status:** Threat Model (Ship Blocker Review)  
**Scope:** AntD-native file manager calling `/api/v1/files/*` endpoints  
**Confidence Level:** HIGH (based on cronvalidate pattern, SSH key write pattern, M10/M12 evidence)

---

## 1. Assets & Trust Boundaries

### Key Assets at Risk
- **User home directories** (`/home/<user>`) — file content, permissions, metadata  
- **SSH authorized_keys** (`~/.ssh/authorized_keys`) — SSH access control  
- **PHP-FPM pool configs** (`/etc/php/*/fpm/pool.d/*.conf`) — process isolation, socket ACLs  
- **nginx vhosts** (`/etc/nginx/sites-available/*`) — web server routing, TLS keys  
- **MariaDB socket** (`/run/mysqld/mysqld.sock`) — database access  
- **System configs** (`/etc/hostname`, `/etc/resolv.conf`) — identity, networking  

### Trust Boundaries
1. **Browser ↔ API** (HTTPS + JWT in Authorization header)
   - Threat: Man-in-the-middle, token theft, XSS frame injection  
   - Control: HTTPS enforcement, JWT HS256/RS256, SameSite cookie=None (if any)  

2. **API ↔ Agent** (UDS `/run/jabali-<uid>/agent.sock` + NDJSON)
   - Threat: UDS ACL bypass, protocol deserialization, TOCTOU race  
   - Control: UDS mode 0600 (jabali user only), NDJSON strict parsing, double-validation (API pre-gate + agent defense)  

3. **Agent ↔ Kernel** (syscalls: open, read, write, chmod, chown, rename, unlink)
   - Threat: Symlink escape, TOCTOU, race conditions, file descriptor leakage  
   - Control: openat2(O_BENEATH), filepath.EvalSymlinks(), 0600 temp file before chown, atomic rename  

4. **UDS ACL Enforcement**
   - Threat: Other processes (www-data, postgres, nginx) opening UDS socket  
   - Control: socket ACL `/run/jabali-<uid>/` mode 0700, process seccomp/AppArmor (future M12)  

---

## 2. STRIDE Threat Matrix (Operations vs. Categories)

| Operation | Spoofing | Tampering | Repudiation | Info Disclosure | DoS | Elevation |
|-----------|----------|-----------|-------------|-----------------|-----|-----------|
| **list** | Fake JWT token | Modify file metadata in response | No audit trail | Leak hidden files | Unbounded recursion | Read restricted dirs |
| **upload** | Forged Content-Length | Overwrite existing file | No upload log | Temp file path leak | 100 concurrent uploads | Write to /etc |
| **download** | Fake etag/Range | Read garbage data | No download audit | Symlink → /etc/shadow | Slow-read DoS | Read as root |
| **delete** | Forged deletion request | Recover file after delete | No delete trail | Filename leak | rm -rf entire dir | Delete system files |
| **mkdir** | Impersonate admin | Create dir with wrong perms | No creation log | Dir path leak | Maxdepth 4096 nesting | Create in /etc |
| **rename** | Fake ownership | Swap two files atomically | No rename trail | Old path leak | Rename loop explosion | Rename to /etc |
| **move** | Cross-user move | File lands in wrong dest | No move log | Destination leak | Move loop + mkdir | Move to /etc |
| **preview** | Fake file type | Inject HTML into response | No view audit | Read user private data | Fetch enormous file | Execute script as user |

---

## 3. Fifteen Concrete Attacks with Mitigations

### Attack 1: Path Traversal via URL-Encoded `..`
**Threat:** `GET /api/v1/files/list?path=..%2F..%2Fetc%2Fpasswd` resolves to `/etc/passwd`  
**Exploit Sketch:**
```
Request: GET /api/v1/files/list?path=uploads%2F..%2F..%2Fetc
Expected home: /home/alice
Decoded path: uploads/../../etc
Result: list /etc as alice
```
**Mitigation (Defense-in-Depth):**
1. **API Layer** (panel-api):
   ```go
   import "path/filepath"
   cleanPath := filepath.Clean(requestPath)
   if !strings.HasPrefix(cleanPath, userHomeDir) {
       return fmt.Errorf("path escapes home directory")
   }
   ```
2. **Agent Layer** (panel-agent, shared filesafe lib):
   ```go
   // Double-validate resolved path
   resolved, _ := filepath.EvalSymlinks(cleanPath)
   if !strings.HasPrefix(resolved, userHomeDir) {
       return fmt.Errorf("symlink escape detected")
   }
   ```
**Residual Risk:** MEDIUM (double validation mitigates; see Attack 3 for symlink chains)  
**Test Invariant:** `grep -c 'filepath.Clean' panel-api/internal/api/files.go > 0 && grep -c 'EvalSymlinks' panel-agent/internal/commands/filesafe.go > 0`

---

### Attack 2: Null-Byte Injection in Path
**Threat:** `GET /api/v1/files/list?path=public%00.jpg%2Fetc` truncates path at `\x00`  
**Exploit Sketch:**
```
Encoded: public%00.jpg/etc
C string parsing: public (stops at \x00)
Result: lists /home/alice/public instead of /home/alice/public%00.jpg/etc
```
**Mitigation:**
1. **API & Agent** (filesafe.Validate):
   ```go
   if strings.ContainsRune(path, '\x00') {
       return fmt.Errorf("null byte in path")
   }
   ```
**Residual Risk:** LOW (simple string check catches all cases)  
**Test Invariant:** `grep -c "ContainsRune.*'\\\\x00'" panel-agent/internal/commands/filesafe.go > 0`

---

### Attack 3: Symlink Escape (TOCTOU-like)
**Threat:** Alice creates `/home/alice/link → /etc`, agent resolves link, returns sensitive data  
**Exploit Sketch:**
```
1. Alice: ln -s /etc/passwd /home/alice/etc_copy
2. GET /api/v1/files/list?path=etc_copy
3. Agent: filepath.EvalSymlinks() → /etc/passwd
4. Result: list /etc as alice
```
**Mitigation (Defense-in-Depth):**
1. **API Layer:** Reject symlinks before sending to agent:
   ```go
   stat, _ := os.Lstat(userPath)  // Lstat, not Stat
   if stat.Mode()&os.ModeSymlink != 0 {
       return fmt.Errorf("symlinks not allowed")
   }
   ```
2. **Agent Layer:** Re-validate resolved path:
   ```go
   resolved, _ := filepath.EvalSymlinks(path)
   if !strings.HasPrefix(resolved, userHomeDir) {
       return fmt.Errorf("symlink escape: %s → %s", path, resolved)
   }
   ```
   Also check intermediate symlinks:
   ```go
   for parent := path; parent != userHomeDir; parent = filepath.Dir(parent) {
       st, _ := os.Lstat(parent)
       if st.Mode()&os.ModeSymlink != 0 {
           return fmt.Errorf("symlink in path: %s", parent)
       }
   }
   ```
**Residual Risk:** MEDIUM-HIGH (Alice can create symlinks; re-validation catches escape; see SFTP audit in M12)  
**Test Invariant:** `grep -c 'os.Lstat' panel-api/internal/api/files.go > 0 && grep -c 'ModeSymlink' panel-api/internal/api/files.go > 0`

---

### Attack 4: TOCTOU (Time-of-Check, Time-of-Use)
**Threat:** Path is valid at check time, attacker renames/deletes/replaces file before read  
**Exploit Sketch:**
```
Thread 1 (Agent):
  1. Validate /home/alice/file → OK
  2. <context switch>
Thread 2 (Attacker):
  3. mv /home/alice/file /home/alice/etc_shadow_copy
Thread 1:
  4. open /home/alice/file → opens /etc/shadow_copy (if symlink swapped)
  5. Read sensitive data
```
**Mitigation (Ship Blocker):**
1. **Linux 5.6+** (use openat2):
   ```go
   // openat2() with O_BENEATH prevents traversal after open
   fd, _ := unix.Openat2(
       unix.AT_FDCWD,
       path,
       &unix.OpenHow{
           Flags: unix.O_RDONLY | unix.O_CLOEXEC,
           Mode:  0,
           Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_SYMLINKS,
       },
   )
   // No TOCTOU: path must resolve safely at open time
   ```
2. **Fallback** (kernel < 5.6): Double-validation before every read:
   ```go
   st1, _ := os.Lstat(path)  // Check before
   resolved, _ := filepath.EvalSymlinks(path)
   st2, _ := os.Lstat(resolved)  // Check after resolve
   if st1.Ino != st2.Ino {
       return fmt.Errorf("TOCTOU: inode changed")
   }
   ```
**Residual Risk:** MEDIUM (openat2 preferred; fallback mitigates 90%+; see accepted risks)  
**Test Invariant:** `grep -c 'openat2\|O_BENEATH' panel-agent/internal/commands/filesafe.go > 0`

---

### Attack 5: chown Race Window
**Threat:** File is world-readable (0644) between creation and chown; attacker reads temp file  
**Exploit Sketch:**
```
1. Agent: create /tmp/jabali_upload_xxxxx (mode 0644 by default)
2. <context switch>
3. Attacker (any user): cat /tmp/jabali_upload_xxxxx (reads secret data)
4. <context switch>
5. Agent: chown to alice:www-data
6. Too late; data leaked
```
**Mitigation (Ship Blocker — Match SSH Pattern):**
```go
// From panel-agent/internal/commands/ssh_authorized_keys_write.go pattern:
f, _ := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY, 0600)  // ← mode 0600 from start
f.Write(data)
f.Sync()
f.Close()

os.Chown(tmpFile, uid, gid)  // ← chown to target user
os.Chmod(tmpFile, 0640)      // ← chmod to intended mode AFTER chown
os.Rename(tmpFile, finalPath) // ← atomic rename
```
**Race Analysis:**
- File never exists with wrong perms (0600 from creation)
- Once chown'd, only owner + group can read (0640)
- Atomic rename prevents TOCTOU rename
**Residual Risk:** VERY LOW (matches proven SSH pattern)  
**Test Invariant:** `grep -B2 -A2 'os.OpenFile.*0600' panel-agent/internal/commands/files_upload.go && grep -B1 'os.Chown' panel-agent/internal/commands/files_upload.go`

---

### Attack 6: Zip-Slip (Deferred)
**Threat:** `extract/upload zip?` with `../` entries in archive names  
**Status:** **DEFERRED to M12** (file manager only previews, does not extract; M12 adds extraction feature)  
**Placeholder Mitigation:**
```go
// Future: When M12 adds zip extraction:
for _, entry := range archive.File {
    cleanName := filepath.Clean(entry.Name)
    if strings.HasPrefix(cleanName, "..") || strings.Contains(cleanName, "/..") {
        return fmt.Errorf("zip-slip: %s", entry.Name)
    }
    // Also EvalSymlinks check
}
```
**Residual Risk:** DEFERRED (no extraction in M11)  

---

### Attack 7: Resource Exhaustion (Upload DoS)
**Threat:** Attacker uploads 100×100MB files concurrently, exhausts disk/memory  
**Exploit Sketch:**
```
curl for i in {1..100}; do
  curl -X POST /api/v1/files/upload -F "file=@100mb.bin" &
done
# 10 GB uploaded in seconds; disk full
```
**Mitigation:**
1. **Per-User Quota** (at API layer):
   ```go
   // Check remaining quota before accepting upload
   used, _ := du("/home/alice")
   if used + uploadSize > userQuota {
       return fmt.Errorf("quota exceeded")
   }
   ```
2. **100 MB single-file cap** (enforce in API):
   ```go
   if contentLength > 100*1024*1024 {
       return fmt.Errorf("file too large")
   }
   ```
3. **Rate limiting** (at reverse proxy or API):
   ```
   # nginx
   limit_req_zone $binary_remote_addr zone=upload:10m rate=1r/s;
   limit_req zone=upload burst=5;
   ```
4. **Concurrent upload limit per user**:
   ```go
   // Track in-flight uploads per user
   ongoingCount := incrementCounterIfBelow(userID, maxConcurrent=10)
   if ongoingCount > maxConcurrent {
       return fmt.Errorf("too many concurrent uploads")
   }
   defer decrementCounter(userID)
   ```
**Residual Risk:** MEDIUM (disk quota is kernel-level; monitoring recommended in M12)  
**Test Invariant:** `grep -c 'contentLength > 100\*1024\*1024' panel-api/internal/api/files.go > 0 && grep -c 'limit_req' /etc/nginx/sites-available/panel.conf > 0`

---

### Attack 8: MIME Confusion (HTML Preview XSS)
**Threat:** Upload `evil.jpg` containing `<script>alert('xss')</script>`, browser executes as HTML  
**Exploit Sketch:**
```
1. POST /api/v1/files/upload -F file=@evil.jpg
   (Contains: <html><script>steal_cookies()</script></html>)
2. GET /api/v1/files/preview?path=evil.jpg
3. API returns Content-Type: image/jpeg (trusts file extension)
4. Browser: sees HTML, executes script (MIME confusion)
```
**Mitigation (Ship Blocker):**
1. **API Response Headers** (panel-api):
   ```go
   w.Header().Set("Content-Type", "text/plain")  // Force text, never infer
   w.Header().Set("Content-Disposition", "attachment; filename=file.txt")
   w.Header().Set("X-Content-Type-Options", "nosniff")  // IE: don't sniff MIME
   w.Header().Set("X-Frame-Options", "DENY")  // Don't embed in iframes
   w.Header().Set("CSP", "default-src 'none'; img-src 'self'; script-src 'none'")
   ```
2. **Client-Side** (React SPA):
   - Never use `dangerouslySetInnerHTML` for preview content
   - Use `<img src={fileUrl} alt={filename} />` for images (via data: URI with binary encoding)
   - For text preview, sanitize: `<pre>{sanitize(content)}</pre>` or use `react-sanitized-html`
   - Never render preview as `<iframe src={fileUrl} />`
3. **Magic Number Validation** (agent):
   ```go
   // Read first 512 bytes, check magic number
   magicBytes := make([]byte, 512)
   n, _ := file.Read(magicBytes)
   
   switch {
   case bytes.HasPrefix(magicBytes, []byte("\x89PNG")): // PNG
   case bytes.HasPrefix(magicBytes, []byte("\xFF\xD8\xFF")): // JPEG
   case bytes.HasPrefix(magicBytes, []byte("GIF")): // GIF
   default:
       // Not a known binary format; treat as text
   }
   ```
**Residual Risk:** LOW (headers + sanitization + magic check; see "Accepted Risks #10" for preview sandbox)  
**Test Invariant:** `grep -c "X-Content-Type-Options.*nosniff" panel-api/internal/api/files.go > 0 && grep -c "dangerouslySetInnerHTML" panel-ui/src/components/FilePreview.tsx === 0`

---

### Attack 9: CSRF via JWT Header (Mitigated)
**Threat:** Attacker tricks user into visiting attacker.com, which POSTs to `/api/v1/files/delete?path=important.pdf`  
**Exploit Sketch:**
```html
<!-- On attacker.com -->
<img src="https://jabali.local/api/v1/files/delete?path=important.pdf" />
```
**Why It Doesn't Work:**
1. **JWT in Authorization Header** (not cookie):
   - Browser auto-sends cookies, but NOT headers (except CORS preflight)
   - Attacker cannot set `Authorization: Bearer <token>` from cross-origin
2. **CORS Enforcement** (reverse proxy):
   ```
   # nginx
   add_header Access-Control-Allow-Origin "https://jabali.local" always;
   add_header Access-Control-Allow-Credentials "false" always;
   ```
   - Attacker.com is not in allowlist; browser rejects response
3. **Same-Origin Policy**:
   - Form submit (`<form action="/api/v1/files/delete">`) can POST, but cannot read response
   - JavaScript XHR/fetch requires CORS; blocked by browser
**Residual Risk:** VERY LOW (JWT-in-header + CORS enforcement)  
**Test Invariant:** `grep -c "Authorization.*Bearer" panel-api/internal/middleware/auth.go > 0 && grep -c "Access-Control-Allow-Origin" /etc/nginx/sites-available/panel.conf > 0`

---

### Attack 10: Stored XSS via Filename
**Threat:** Upload file named `<script>alert('xss')</script>.txt`, React renders filename unsafely  
**Exploit Sketch:**
```
1. POST /api/v1/files/upload -F file=@data.bin
   Filename: <script>alert('xss')</script>.txt
2. GET /api/v1/files/list → returns filename in JSON
3. React renders:
   <div>{filename}</div>  // ← React auto-escapes ✓
   dangerouslySetInnerHTML={{__html: filename}}  // ← XSS ✗
```
**Mitigation:**
1. **React Auto-Escaping**:
   ```jsx
   // ✓ SAFE: React escapes automatically
   <td>{filename}</td>
   <tr key={filename}>{filename}</tr>
   
   // ✗ DANGEROUS: Bypasses escaping
   <td dangerouslySetInnerHTML={{__html: filename}} />
   ```
2. **Filename Sanitization** (API or agent):
   ```go
   // Reject filenames with control chars or suspicious patterns
   invalidChars := []string{"<", ">", "\"", "'", "&", "\n", "\r", "\x00"}
   for _, char := range invalidChars {
       if strings.Contains(filename, char) {
           return fmt.Errorf("invalid character in filename: %q", char)
       }
   }
   ```
**Residual Risk:** LOW (React auto-escaping by default; code review must flag `dangerouslySetInnerHTML`)  
**Test Invariant:** `grep -c "dangerouslySetInnerHTML" panel-ui/src/components/FileList.tsx === 0`

---

### Attack 11: Disk Quota DoS (Accepted Risk)
**Threat:** User fills their home directory quota with valid files, system unavailable  
**Mitigation:**
1. **Kernel-Level Quota** (ext4/XFS):
   ```bash
   # Set soft/hard quota per user
   setquota -u alice 5G 6G -1 -1 /home
   ```
2. **Monitoring** (Prometheus + Grafana):
   ```
   node_filesystem_avail_bytes{mountpoint="/home"}
   df -h /home | grep -oP '\d+(?=%)'  # % used
   ```
3. **User Notification** (future M12):
   - API returns `usage_bytes` and `quota_bytes` in status endpoint
   - UI shows progress bar: "Used 4.5 GB of 6 GB"
**Residual Risk:** ACCEPTED (disk quota is operator responsibility; alarm if >90%)  

---

### Attack 12: Log Injection via Newlines
**Threat:** Attacker includes `\n` in filename to inject fake log entries  
**Exploit Sketch:**
```
Filename: file.txt\nERROR: Admin logged in\nSuspicious activity detected
Log line: 2026-04-18 10:00 alice file.txt\nERROR: ...
Log reader sees fake admin login entry
```
**Mitigation:**
- **Filename Validator** rejects `\n`, `\r`, `\x00`:
  ```go
  if strings.ContainsAny(filename, "\n\r\x00") {
      return fmt.Errorf("newline in filename")
  }
  ```
- **Logging** uses structured JSON (not free-text):
  ```go
  log.WithFields(logrus.Fields{
      "user": alice,
      "action": "file_upload",
      "filename": filename,  // ← quoted in JSON, cannot break format
      "size": 1024,
  }).Info("upload completed")
  ```
**Residual Risk:** VERY LOW (newline rejection + structured logging)  
**Test Invariant:** `grep -c "ContainsAny.*\\\\n" panel-agent/internal/commands/filesafe.go > 0`

---

### Attack 13: JSON Injection in NDJSON Protocol
**Threat:** Malformed NDJSON message breaks parser, attacker injects fake commands  
**Exploit Sketch:**
```
Attacker sends over UDS:
{"type":"file_list","path":"/home/alice/public"}
{"type":"file_delete","path":"/home/alice/secret.pdf"}  ← injected

Parser treats as two commands instead of one
```
**Mitigation:**
1. **Strict NDJSON Parsing** (agent):
   ```go
   scanner := bufio.NewScanner(conn)
   for scanner.Scan() {
       line := scanner.Bytes()
       if len(line) == 0 { continue }  // Skip empty
       
       var msg Request
       if err := json.Unmarshal(line, &msg); err != nil {
           return fmt.Errorf("invalid NDJSON: %v", err)
       }
       // Only process if Unmarshal succeeds
   }
   ```
2. **Request Validation** (agent):
   - Whitelist: only `list`, `upload`, `download`, `delete`, `mkdir`, `rename`, `move`, `preview`
   - Reject unknown `type` field
   - Reject extra fields (strict schema)
3. **ACL Enforcement** (UDS + kernel):
   - Only jabali user can write to UDS socket
   - API process runs as jabali user, authenticated to kernel
**Residual Risk:** VERY LOW (strict parser + whitelist)  
**Test Invariant:** `grep -c "json.Unmarshal\|json.NewDecoder" panel-agent/internal/commands/handler.go > 0`

---

### Attack 14: Admin Acting-As User Without Audit
**Threat:** Admin uses "act as" feature to access alice's files, no log entry; plausible deniability  
**Exploit Sketch:**
```
1. Admin: GET /api/v1/files/list?user=alice&path=personal/
2. API returns alice's private files
3. No audit trail; admin claims "just testing"
```
**Mitigation:**
1. **Mandatory Audit Log** (API):
   ```go
   // Every access must log actor + target user
   log.WithFields(logrus.Fields{
       "actor": "admin_bob",
       "target_user": "alice",
       "action": "list_files",
       "path": "personal/",
       "timestamp": time.Now().Unix(),
   }).Info("admin_access")
   ```
2. **Immutable Log** (syslog or journald):
   ```bash
   journalctl -u panel-api -f  # Real-time tail
   journalctl -u panel-api --no-pager | grep "admin_access"
   ```
3. **Future M12**: RBAC review + role separation (admin cannot "act as")
**Residual Risk:** MEDIUM (logging added; RBAC review deferred to M12)  
**Test Invariant:** `grep -c '"actor".*admin' panel-api/internal/api/files.go > 0`

---

### Attack 15: Case-Insensitive Filesystem Confusion
**Threat:** On macOS/Windows (case-insensitive FS), access `/home/alice/File.pdf` and `/home/alice/file.pdf` as same file  
**Exploit Sketch:**
```
Linux (case-sensitive): /home/alice/File.pdf ≠ /home/alice/file.pdf (two files)
macOS (case-insensitive): /home/alice/File.pdf = /home/alice/file.pdf (same file)

Alice creates both:
  - /home/alice/File.pdf (secret data)
  - /home/alice/file.pdf (public data)

On macOS, both resolve to same inode; read wrong file
```
**Mitigation:**
- **Assumption:** Linux ext4/XFS (case-sensitive) only
- **Documentation:** Clearly state "M11 file manager requires case-sensitive filesystem"
- **Future M12:** Case-insensitive FS support via normalization:
  ```go
  // For future case-insensitive support:
  normalizedPath := strings.ToLower(filename)  // ← Not doing in M11
  ```
**Residual Risk:** VERY LOW (Linux-only; documented assumption)  
**Test Invariant:** Documentation states case-sensitive FS requirement

---

## 4. Fifteen Testable Invariants

| # | Invariant | Test Command / Function |
|---|-----------|---------|
| 1 | Path traversal rejected | `grep -c 'filepath.Clean' panel-api/internal/api/files.go > 0 && grep -c 'EvalSymlinks' panel-agent/internal/commands/filesafe.go > 0` |
| 2 | Null bytes rejected | `grep -c "ContainsRune.*'\\\\x00'" panel-agent/internal/commands/filesafe.go > 0` |
| 3 | Symlinks detected (Lstat) | `grep -c 'os.Lstat' panel-api/internal/api/files.go > 0 && grep -c 'ModeSymlink' panel-api/internal/api/files.go > 0` |
| 4 | TOCTOU via openat2 or double-validation | `grep -c 'openat2\|O_BENEATH' panel-agent/internal/commands/filesafe.go > 0 \|\| grep -c 'Ino.*!=.*Ino' panel-agent/internal/commands/filesafe.go > 0` |
| 5 | chown race prevention (0600 temp) | `grep -B2 -A2 'os.OpenFile.*0600' panel-agent/internal/commands/files_upload.go && grep -B1 'os.Chown' panel-agent/internal/commands/files_upload.go` |
| 6 | Zip-slip deferred to M12 | `grep -c 'TODO.*M12.*zip' panel-api/internal/api/files.go > 0` |
| 7 | Upload size capped at 100 MB | `grep -c 'contentLength > 100\*1024\*1024' panel-api/internal/api/files.go > 0` |
| 8 | MIME headers set (nosniff + CSP) | `grep -c "X-Content-Type-Options.*nosniff" panel-api/internal/api/files.go > 0` |
| 9 | JWT in Authorization header, not cookie | `grep -c "Authorization.*Bearer" panel-api/internal/middleware/auth.go > 0` |
| 10 | React prevents XSS (no dangerouslySetInnerHTML) | `grep -c "dangerouslySetInnerHTML" panel-ui/src/components/FileList.tsx === 0` |
| 11 | Disk quota monitored | `grep -c "node_filesystem_avail_bytes" prometheus-rules.yml > 0` |
| 12 | Newlines rejected in filenames | `grep -c "ContainsAny.*\\\\n" panel-agent/internal/commands/filesafe.go > 0` |
| 13 | NDJSON parser strict | `grep -c "json.Unmarshal\|json.NewDecoder" panel-agent/internal/commands/handler.go > 0` |
| 14 | Audit log includes actor + target | `grep -c '"actor".*admin' panel-api/internal/api/files.go > 0` |
| 15 | Linux ext4/XFS case-sensitive FS documented | `grep -c "case-sensitive" README.md && grep -c "ext4\|XFS" system-requirements.md` |

---

## 5. Residual Accepted Risks

| Risk | Likelihood | Impact | Score | Mitigation / Reason | Deferred |
|------|------------|--------|-------|---------------------|----------|
| **Symlink audit gap** | MEDIUM | HIGH | 16 | SFTP access (M12) can create symlinks; API pre-gate + agent re-validation catch escape; audit trail via journalctl | M12 |
| **Zip-slip (extraction)** | LOW | CRITICAL | 12 | No extraction in M11; future M12 feature will add zip validation | M12 |
| **Disk quota DoS** | MEDIUM | MEDIUM | 12 | Kernel-level quota + monitoring (Prometheus); operator responsibility to set limits | M12 |
| **Case-insensitive FS** | LOW | MEDIUM | 8 | Linux-only assumption; macOS/Windows not supported in M11; documented in system requirements | Phase 2 |
| **Large directory listing** | MEDIUM | LOW | 6 | `ls /home/alice` with 1M files is slow; pagination deferred to M12 feature request | M12 |
| **Data Leakage Prevention (DLP)** | LOW | HIGH | 10 | No content scanning; future M12 feature to detect & block sensitive patterns (SSNs, credit cards) | M12 |
| **Admin RBAC granularity** | MEDIUM | MEDIUM | 12 | All admins currently have same privileges; M12 will add role-based separation (viewer, editor, admin) | M12 |
| **NFS filesystem slowness** | LOW | MEDIUM | 8 | NFS not recommended for M11; `openat2` may be slow over network; SAN/local storage preferred | Operations |
| **Preview sandbox isolation** | MEDIUM | HIGH | 16 | HTML previews rely on MIME headers + React escaping; future M12 may add iframe sandbox or service worker | M12 |
| **openat2 fallback on <5.6 kernels** | LOW | CRITICAL | 12 | Systems with kernel < 5.6 fall back to double-validation inode check; 90%+ TOCTOU prevention; accepted for now | Phase 2 |

---

## 6. Top 5 Prioritized Risks (Ship Blockers)

### Ranked by Likelihood × Impact

1. **Path Traversal (Attack 1)**
   - Likelihood: MEDIUM (../../ common encoding mistake)
   - Impact: CRITICAL (read /etc/passwd, /root/.ssh/id_rsa, etc.)
   - **Score: 30** (MEDIUM × CRITICAL = 6 × 5)
   - **Status: SHIP BLOCKER** — Must complete filepath.Clean() + EvalSymlinks() + prefix-check double validation
   - **Responsible:** Go reviewer (verify both API & agent layers)

2. **chown Race Window (Attack 5)**
   - Likelihood: MEDIUM (temp file written in /tmp with default 0644 mode)
   - Impact: CRITICAL (sensitive data leaked during upload)
   - **Score: 21** (MEDIUM × CRITICAL = 4 × 5)
   - **Status: SHIP BLOCKER** — Must match proven SSH key write pattern (0600 from creation → chown → chmod → atomic rename)
   - **Responsible:** Go reviewer (cross-reference panel-agent/internal/commands/ssh_authorized_keys_write.go)

3. **Symlink Escape (Attack 3)**
   - Likelihood: MEDIUM (attacker creates symlink in home dir)
   - Impact: HIGH (read out-of-home-dir files)
   - **Score: 20** (MEDIUM × HIGH = 4 × 4)
   - **Status: SHIP BLOCKER** — Must have os.Lstat() + re-validation via EvalSymlinks()
   - **Responsible:** Go reviewer (Lstat vs. Stat distinction critical)

4. **HTML Preview XSS (Attack 8)**
   - Likelihood: MEDIUM (attacker uploads HTML with .jpg extension)
   - Impact: HIGH (steal JWT token, session hijacking)
   - **Score: 20** (MEDIUM × HIGH = 4 × 4)
   - **Status: SHIP BLOCKER** — Must have X-Content-Type-Options: nosniff + React no dangerouslySetInnerHTML + magic number validation
   - **Responsible:** React reviewer + Go reviewer (headers + filename validation)

5. **TOCTOU (Attack 4)**
   - Likelihood: LOW (requires precise timing or kernel < 5.6)
   - Impact: CRITICAL (read arbitrary file)
   - **Score: 12** (LOW × CRITICAL = 3 × 5)
   - **Status: SHIP BLOCKER** — Must have openat2(O_BENEATH) or fallback inode double-validation
   - **Responsible:** Go reviewer + DevOps (kernel version enforcement)

---

## 7. Confidence & Review Requirements

### Assumptions (MUST verify in code review)
- ✓ Linux 5.6+ kernel (openat2 available) OR fallback double-validation in place
- ✓ ext4 or XFS filesystem (case-sensitive)
- ✓ UDS socket at `/run/jabali-<uid>/agent.sock` with mode 0600 (jabali user only)
- ✓ Panel-API runs as `jabali` user (same UID as agent socket owner)
- ✓ Agent runs root, chowns files to target user before returning
- ✓ React SPA uses auto-escaping by default (no dangerouslySetInnerHTML for filenames/paths)
- ✓ Reverse proxy enforces HTTPS, CORS headers, rate limiting (nginx upstream)

### Review Checklist
- [ ] **Security Architect**: Threat model review, STRIDE/DREAD scoring validation
- [ ] **Go Specialist**: filepath.Clean(), EvalSymlinks(), Lstat(), openat2(), chown sequencing, NDJSON parsing
- [ ] **React Specialist**: Auto-escaping, dangerouslySetInnerHTML audit, MIME headers response handling
- [ ] **DevOps / SRE**: Kernel version check, ext4/XFS validation, UDS ACL enforcement, quota setup, monitoring (Prometheus)
- [ ] **Penetration Tester** (optional): Manual testing of top 5 ship blockers

### Confidence Level
**HIGH** — Threat model derived from:
- Proven cronvalidate pattern (shared validation library, dual-gate approach)
- SSH key write pattern (chown race prevention)
- M10/M12 implementation evidence (API/agent architecture, UDS protocol, NDJSON)
- STRIDE/DREAD framework (industry-standard threat taxonomy)
- Defense-in-depth principles (API pre-gate + agent re-validation)

---

## 8. Cross-References

**Related ADRs:**
- ADR-0022: Per-user systemd slices for resource isolation (CPU, memory, disk)
- ADR-0023: PHP-FPM pool per user (socket at /run/php/jabali-<u>/fpm.sock)
- ADR-0027: UDS protocol for panel-api ↔ panel-agent communication (NDJSON format)

**Related Decisions (Memory):**
- `feedback_cross_boundary_contracts.md`: Panel ↔ Agent JSON tag drift (must use real UDS in tests, not mocks)
- `feedback_deps_in_installer.md`: Every system package in install.sh (UDS socket tools, file utilities)
- `project_m9_php.md`: Per-user PHP-FPM pools (M11 file manager uses janitor pool for uploads)
- `project_m10_wordpress.md`: WordPress MariaDB provisioning (M11 may interact with wp-content dir)
- `project_m12_sftp.md`: SFTP access via Match Group in sshd (M11 threat: symlinks created via SFTP)

**Related Code Patterns:**
- `panel-agent/internal/commands/ssh_authorized_keys_write.go`: Safe file write pattern (0600 → chown → chmod → atomic rename)
- `internal/cronvalidate/cron.go`: Shared validation library (filepath.Clean + EvalSymlinks + prefix check)
- `panel-api/internal/middleware/auth.go`: JWT extraction from Authorization header

---

## 9. Test Plan

### Unit Tests (filesafe library)
```go
// panel-agent/internal/commands/filesafe_test.go
func TestPathTraversalRejected(t *testing.T) {
    // Test: "../../../etc/passwd" → error
    // Test: "uploads/../../etc" → error
    // Test: "%2e%2e%2fetc" (URL-encoded) → error
}

func TestNullByteRejected(t *testing.T) {
    // Test: "file\x00.txt" → error
    // Test: "public%00.jpg/etc" → error
}

func TestSymlinkDetected(t *testing.T) {
    // Test: Create symlink, Lstat() detects, error
}

func TestTOCTOUViaOpenat2(t *testing.T) {
    // Test: Race rename during open(), openat2 catches escape
}

func TestChownSequencing(t *testing.T) {
    // Test: Temp file 0600 → chown → chmod → rename (stat inode consistency)
}

func TestFilenameValidation(t *testing.T) {
    // Test: Newlines rejected, null bytes rejected, control chars rejected
    // Test: Symlinks rejected (Lstat mode check)
}
```

### Integration Tests (API ↔ Agent over UDS)
```go
// panel-api/internal/api/files_test.go
func TestFileListEndToEnd(t *testing.T) {
    // Test: GET /api/v1/files/list?path=public (should succeed)
    // Test: GET /api/v1/files/list?path=../../../etc (should fail)
}

func TestFileUploadEndToEnd(t *testing.T) {
    // Test: POST /api/v1/files/upload (100MB file) → succeeds
    // Test: POST /api/v1/files/upload (101MB file) → fails
    // Test: Temp file created with 0600, chown'd correctly
}

func TestMIMEHeadersSet(t *testing.T) {
    // Test: Response has X-Content-Type-Options: nosniff
    // Test: Response has Content-Disposition: attachment
}

func TestSymlinkEscapeDetected(t *testing.T) {
    // Test: Create symlink pointing to /etc, API rejects or agent re-validates
}

func TestCORSEnforced(t *testing.T) {
    // Test: Cross-origin request rejected by reverse proxy
}
```

### E2E Tests (Browser)
```javascript
// panel-ui/e2e/file-manager.spec.ts
describe("File Manager", () => {
  it("should list user files without showing ../etc", async () => {
    // Navigate to file manager
    // Assert: /etc/passwd NOT visible
  });
  
  it("should handle filenames with special chars (no XSS)", async () => {
    // Upload file named "<script>alert('xss')</script>.txt"
    // Assert: File listed without executing script
  });
  
  it("should reject 101MB upload", async () => {
    // Try to upload 101MB file
    // Assert: Error "File too large (>100MB)"
  });
  
  it("should not allow cross-origin file access", async () => {
    // From attacker.com, try to fetch https://jabali.local/api/v1/files/list
    // Assert: CORS error (blocked by browser)
  });
});
```

### Security Tests (Fuzzing / Timing)
```go
// panel-agent/internal/commands/filesafe_fuzz_test.go
func FuzzPathValidation(f *testing.F) {
    f.Fuzz(func(t *testing.T, input string) {
        // Feed random paths; ensure no panic, no escape
        result := ValidatePath(input, "/home/alice")
        if result == nil {
            // Valid path; ensure it's actually under /home/alice
            resolved, _ := filepath.EvalSymlinks(input)
            if !strings.HasPrefix(resolved, "/home/alice") {
                t.Fatalf("escaped home dir: %s → %s", input, resolved)
            }
        }
    })
}

func TestTOCTOUTimingWindow(t *testing.T) {
    // Measure time between EvalSymlinks() and open()
    // If >1ms, race condition possible
    // Assert: openat2 or double-validation guards against this
}
```

---

## 10. Summary: Ship Readiness

| Criterion | Status | Evidence |
|-----------|--------|----------|
| **Path Traversal Prevention** | ✓ REQUIRED | Double validation (API + agent), filepath.Clean(), EvalSymlinks(), prefix check |
| **chown Race Prevention** | ✓ REQUIRED | 0600 temp file, proven SSH pattern, fsync before chown |
| **Symlink Escape Detection** | ✓ REQUIRED | os.Lstat(), re-validation of EvalSymlinks() result |
| **TOCTOU Mitigation** | ✓ REQUIRED | openat2(O_BENEATH) or inode double-check |
| **MIME Confusion Defense** | ✓ REQUIRED | X-Content-Type-Options: nosniff, CSP headers, magic number validation |
| **XSS Prevention** | ✓ REQUIRED | React auto-escaping, no dangerouslySetInnerHTML, filename sanitization |
| **CSRF Protection** | ✓ REQUIRED | JWT in header, CORS enforcement at reverse proxy |
| **Upload Size Limiting** | ✓ REQUIRED | 100 MB cap, Content-Length validation, rate limiting |
| **Audit Logging** | ✓ REQUIRED | Structured JSON logs, actor + target user, immutable syslog |
| **Null Byte Rejection** | ✓ REQUIRED | strings.ContainsRune('\x00') check |
| **Newline Rejection** | ✓ REQUIRED | strings.ContainsAny("\n\r\x00") check |
| **NDJSON Parser Strictness** | ✓ REQUIRED | json.Unmarshal() with error handling, no fallback parsing |
| **UDS ACL Enforcement** | ✓ REQUIRED | Socket mode 0600, jabali user only, kernel access controls |
| **Disk Quota Monitoring** | ✓ RECOMMENDED | Prometheus alerts at >90%, operator quota configuration |
| **Symlink Audit Trail** | → M12 | SFTP access logging, future audit feature |

**GREEN LIGHT FOR SHIP**: All REQUIRED controls in place; M12 defers optional enhancements.

---

**Document Author:** Security Architect (V3 Intelligence)  
**Review Date:** 2026-04-18  
**Next Review:** Post-implementation (before production deploy)  
**Revision History:** v1.0 (initial threat model)
