<?php
/**
 * SSO handler for phpMyAdmin.
 *
 * Validates an SSO token via the panel's UDS socket (/run/jabali-panel/sso.sock)
 * and populates the SignonSession to enable phpMyAdmin access without
 * interactive login.
 *
 * Flow:
 *   1. Read the token from query parameter (?token=...)
 *   2. POST the token to the UDS validator
 *   3. Parse the response (MySQL credentials + DB name)
 *   4. Populate $_SESSION with the phpMyAdmin SignonSession structure
 *   5. Redirect to the database view
 *
 * Security:
 *   - Validates token format before sending
 *   - Never echoes validator response into output
 *   - Encodes all output (Location header)
 *   - Fails closed on any error (400 + exit)
 *   - Sets secure session cookies (secure, httponly, samesite=Lax)
 */

// Emit a small HTML page that explains the error and tells the user to
// relaunch from the panel. We reach this file in three ways: the happy
// SSO handoff (valid token + db), a direct visit (no token), and
// phpMyAdmin bouncing back here after a session timeout because we set
// SignonURL = /phpmyadmin/sso.php. The last two both look the same to
// this script, so show the same page rather than a blank 400.
function jabali_sso_fail(string $reason): void {
    http_response_code(400);
    header('Content-Type: text/html; charset=utf-8');
    header('Cache-Control: no-store');
    $safe = htmlspecialchars($reason, ENT_QUOTES, 'UTF-8');
    echo <<<HTML
<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>phpMyAdmin — session expired</title>
<style>
 body{font-family:system-ui,-apple-system,Segoe UI,Arial,sans-serif;background:#fafafa;color:#222;margin:0;padding:2rem;display:flex;justify-content:center}
 main{max-width:520px;background:#fff;border:1px solid #e5e5e5;border-radius:8px;padding:1.75rem 2rem;box-shadow:0 1px 3px rgba(0,0,0,.05)}
 h1{margin:0 0 .5rem;font-size:1.15rem}
 p{margin:.5rem 0;line-height:1.5}
 code{background:#f2f2f2;padding:0 .25rem;border-radius:3px;font-size:.95em}
 .hint{color:#666;font-size:.95em}
</style></head><body><main>
<h1>Session expired</h1>
<p>This page is the phpMyAdmin SSO entry point. It needs a short-lived token from the panel and cannot be opened directly.</p>
<p class="hint">Return to the Jabali panel and click <strong>Open in phpMyAdmin</strong> on the database row to start a fresh session.</p>
<p class="hint">Reason: {$safe}</p>
</main></body></html>
HTML;
    exit;
}

// Validate token format: alphanumeric + dashes + underscores, 32-128 chars
// (base64url-encoded 32-byte value = 43 chars; allow room for future expansion)
if (!isset($_GET['token']) || !preg_match('/^[A-Za-z0-9_-]{32,128}$/', $_GET['token'])) {
    jabali_sso_fail('missing or malformed token');
}

$token = $_GET['token'];
$socket_path = 'unix:///run/jabali-panel/sso.sock';

// Connect to the UDS validator socket
$socket = stream_socket_client($socket_path, $errno, $errstr, 5);
if ($socket === false) {
    jabali_sso_fail('cannot reach SSO validator socket');
}

// Build a minimal HTTP/1.1 POST request to the validator
// (no Guzzle, no curl; keep dependencies at zero)
$request_body = json_encode(['token' => $token]);
$request = "POST /sso/phpmyadmin/validate HTTP/1.1\r\n"
         . "Host: localhost\r\n"
         . "Content-Type: application/json\r\n"
         . "Content-Length: " . strlen($request_body) . "\r\n"
         . "Connection: close\r\n"
         . "\r\n"
         . $request_body;

fwrite($socket, $request);

// Read the response
$response_raw = '';
while (!feof($socket)) {
    $response_raw .= fread($socket, 4096);
}
fclose($socket);

// Parse HTTP response: split headers from body
$parts = explode("\r\n\r\n", $response_raw, 2);
if (count($parts) < 2) {
    jabali_sso_fail('malformed validator response');
}

list($headers, $body) = $parts;

// Check for 200 OK status code
if (strpos($headers, '200') === false) {
    jabali_sso_fail('validator rejected token (expired or already used)');
}

// Parse JSON response body
$resp = json_decode($body, true);
if (!is_array($resp) || !isset($resp['user'], $resp['password'], $resp['host'], $resp['port'], $resp['db'], $resp['only_db'])) {
    jabali_sso_fail('unexpected validator payload');
}

// Verify response data types (prevent injection)
if (!is_string($resp['user']) || !is_string($resp['password']) ||
    !is_string($resp['host']) || !is_int($resp['port']) ||
    !is_string($resp['db']) || !is_string($resp['only_db'])) {
    jabali_sso_fail('validator payload type mismatch');
}

// Set secure session cookie parameters BEFORE session_start. The `path`
// must match $cfg['CookiePath'] in /opt/phpmyadmin/current/config.inc.php
// (currently '/phpmyadmin/'). If they differ, the browser stores two
// separate SignonSession cookies — one at "/" written by this script,
// one at "/phpmyadmin/" written by phpMyAdmin's index.php — and
// phpMyAdmin reads the narrower-scope one, misses the signon payload
// we set, and 302-bounces back here. That was the "immediately session
// expired after clicking Open in phpMyAdmin" bug.
session_set_cookie_params([
    'path'     => '/phpmyadmin/',
    'secure'   => true,
    'httponly' => true,
    'samesite' => 'Lax',
]);

// Start the session using the SignonSession name
session_name('SignonSession');
session_start();

// Populate the phpMyAdmin signon session using the keys that
// phpMyAdmin's AuthenticationSignon plugin actually reads.
// Reference: libraries/classes/Plugins/Auth/AuthenticationSignon.php in
// the phpMyAdmin 5.2.x source — readCredentials() bails (returns false)
// when $_SESSION['PMA_single_signon_user'] is unset, which makes
// index.php 302-bounce back to SignonURL. The previous implementation
// wrote $_SESSION['cfg']['Server'][1] and PMA_Auth_provider instead,
// keys the plugin ignores — so every click landed on the session-expired
// page immediately after a successful token handoff.
$_SESSION['PMA_single_signon_user']     = $resp['user'];
$_SESSION['PMA_single_signon_password'] = $resp['password'];
$_SESSION['PMA_single_signon_host']     = $resp['host'];
$_SESSION['PMA_single_signon_port']     = (string) $resp['port'];
// cfgupdate lets signon override per-request cfg values; we scope the
// visible database list to the one the user is SSOing into so the user
// cannot browse sibling databases they do not own.
$_SESSION['PMA_single_signon_cfgupdate'] = [
    'only_db' => $resp['only_db'],
];

// Redirect to phpMyAdmin with the database pre-selected.
// urlencode ensures the DB name is safe in the Location header.
header('Location: /phpmyadmin/index.php?db=' . urlencode($resp['db']));
exit;
?>
