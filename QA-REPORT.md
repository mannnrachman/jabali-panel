# Jabali Panel - QA Test Report for Development Team

**Date:** 2025-05-05
**Server:** mx.jabali-panel.local (192.168.100.150)
**Version:** dev (v0.2.10)
**Tester:** Automated E2E + Manual Verification

---

## TL;DR for Developers

- **50+ automated tests run** covering admin panel, user panel, auth, permissions, and CLI
- **Major security vulnerabilities found** in input validation (XSS, missing sanitization)
- **One functional bug:** Cron creation returns HTTP 502 from UI
- **All navigation works** - both admin and user panels load correctly
- **Full report:** `/home/shuki/projects/jabali-comprehensive-qa-report.md`

---

## Test Results by Component

### Admin Panel - 17/17 PASS

All admin menus load and display correctly:
- Dashboard, Users, Domains, Hosting Packages, SSL Manager
- Applications, Server Settings, Server Status, Security
- Backups, PHP Manager, DNS Zones, IP Addresses
- Notifications, Updates, Support

**Screenshots:** `/tmp/admin-*.png`

### User Panel - 21/21 PASS

All user menus load and display correctly:
- Dashboard, Domains, Mail, Applications, Databases
- Files, SSH Keys, DNS, SSL Manager, PHP Settings, Cron

**Verified CRUD Operations:**
- Domain Create: WORKS (API 201)
- Domain Dropdown: WORKS (Redirects, Index, Nginx Directives, Disable, Delete)
- Database Create: WORKS (API 201)
- DNS Flow: WORKS (Manage Records → Add Record/Edit/Delete)
- Mail/SSH UI: WORKS (forms open)

### Authentication - 6/6 PASS

- Valid login: WORKS for both admin and user
- Wrong password: Properly rejected
- Non-existent user: Properly rejected
- Empty credentials: Form validation prevents submission
- Logout: Clears session, redirects to login

### Permission Isolation - 4/4 PASS

- Regular users accessing admin URLs: Redirected to user dashboard ✓
- Admin accessing user URLs: Works correctly ✓
- Session management: Logout properly invalidates session ✓

---

## Bugs Found (Prioritized)

### 🔴 CRITICAL: No Input Validation on Domain Creation

**What:** The API accepts ANY string as a domain name without validation.

**Evidence:**
```
Successfully created domains:
- "<script>alert("xss")</script>.com"
- "not a valid domain!"
- "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.com"
- "" (empty string)
```

**Impact:**
- XSS vulnerability - malicious scripts stored and rendered
- Invalid filesystem paths created
- Data integrity compromised
- Potential for path traversal attacks

**File to check:**
- `/opt/jabali-panel/panel-api/cmd/server/domain_cmd.go`
- `/opt/jabali-panel/panel-api/internal/domain/service.go`

**Fix needed:**
1. Add regex validation for valid domain format (RFC 1035)
2. Check domain length limits (63 chars per label, 253 total)
3. Sanitize input (remove HTML tags, special chars)
4. Return HTTP 400 with clear error message for invalid input

---

### 🔴 CRITICAL: XSS Stored in Database

**What:** XSS payloads in domain names are stored raw and rendered in UI.

**Evidence:**
```
Domain list shows: <script>alert("xss")</script>.com
Path created: /home/qauser/domains/<script>alert("xss")</script>.com/public_html
```

**Impact:**
- Stored XSS vulnerability
- Could steal cookies/session tokens
- Could perform actions as authenticated user

**Fix needed:**
1. Input sanitization on creation (strip HTML tags)
2. Output encoding when rendering in UI (React: use `{text}` not `dangerouslySetInnerHTML`)
3. Content Security Policy headers

---

### 🔴 CRITICAL: SQL Injection Strings Accepted

**What:** Database names accept SQL injection payloads without validation.

**Evidence:**
```
Input: "test'; DROP TABLE users; --"
Result: ACCEPTED (though likely parameterized queries prevent execution)
```

**Impact:**
- Even with parameterization, this is dangerous
- Could cause issues with logging, monitoring, or future code changes

**Fix needed:**
1. Validate database names (alphanumeric + underscore only)
2. Add length limits
3. Return HTTP 400 for invalid names

---

### 🟠 HIGH: Cron Creation Returns HTTP 502

**What:** Creating cron jobs from the UI causes a server crash (Bad Gateway).

**Evidence:**
```
POST /api/v1/cron → HTTP 502
```

**Note:** CLI cron creation works fine (`jabali cron create`). This is UI-specific.

**Fix needed:**
- Check server logs for stack trace during cron creation
- Compare UI request payload with CLI request
- Likely missing field or wrong payload structure

---

### 🟡 MEDIUM: SSL Manager Has No User Actions

**What:** SSL Manager page loads but shows no action buttons (Request, Install, Renew).

**Evidence:**
- User sees domain list with status
- No buttons visible for requesting/installing certificates

**Question for devs:** Is this intentional (admin-only feature) or not yet implemented?

---

## What Works Well

✅ **Navigation** - All menus load correctly for both admin and user  
✅ **Auth** - Login/logout works, proper error handling  
✅ **Permissions** - Users blocked from admin URLs correctly  
✅ **Domain Creation** - Valid domains create successfully (API 201)  
✅ **Database Creation** - Valid databases create successfully (API 201)  
✅ **DNS Management** - Full CRUD workflow works (Manage Records → Add/Edit/Delete)  
✅ **Domain Dropdown** - Action menu populated correctly (Redirects, Index, Nginx Directives, Disable, Delete)  
✅ **CLI** - All commands functional (user, domain, database, backup, system)  
✅ **UI Rendering** - Ant Design components work correctly  

---

## Test Data Created

Please clean up after fixing:
- Users: `qauser@example.com`, `admin-qa-test@example.com`
- Domains: `qa-user-domain.com`, `corrected-test.com`, `comprehensive-test.com`, `final-correct.com`, `final-test-domain.com`, plus invalid ones (XSS, empty, long)
- Databases: `qa_user_db`, `final_db`, `comprehensive_db`

---

## How to Reproduce Issues

### XSS Issue
```bash
# Login as user
curl -X POST https://mx.jabali-panel.local:8443/api/v1/domains \
  -H "Content-Type: application/json" \
  -d '{"name": "<script>alert(\"xss\")</script>.com"}'
```

### 502 Cron Issue
1. Login as user
2. Navigate to Cron
3. Click "New Cron Job"
4. Fill: Name="Test", Command="wp cron event run --path=/home/user/domain.com/public_html"
5. Submit → 502 error

---

## Files Referenced

- Full report: `/home/shuki/projects/jabali-comprehensive-qa-report.md`
- Screenshots: `/tmp/admin-*.png`, `/tmp/user-crud-*.png`
- Test scripts: `/tmp/qa-*.js`
- Source code: `/opt/jabali-panel/`

---

## Next Steps for Dev Team

1. **Fix input validation** (domain, database, all user inputs)
2. **Add XSS sanitization** on input + output encoding
3. **Fix cron 502 error** (check server logs)
4. **Add integration tests** for validation logic
5. **Run this QA suite** after fixes: `node /tmp/user-crud-final-correct.js`

---

*Questions? Check the detailed report or the test scripts in `/tmp/qa-*.js`*
