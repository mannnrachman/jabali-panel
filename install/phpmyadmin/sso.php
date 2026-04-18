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

// Validate token format: alphanumeric + dashes + underscores, 32-128 chars
// (base64url-encoded 32-byte value = 43 chars; allow room for future expansion)
if (!isset($_GET['token']) || !preg_match('/^[A-Za-z0-9_-]{32,128}$/', $_GET['token'])) {
    http_response_code(400);
    exit;
}

$token = $_GET['token'];
$socket_path = 'unix:///run/jabali-panel/sso.sock';

// Connect to the UDS validator socket
$socket = stream_socket_client($socket_path, $errno, $errstr, 5);
if ($socket === false) {
    http_response_code(400);
    exit;
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
    http_response_code(400);
    exit;
}

list($headers, $body) = $parts;

// Check for 200 OK status code
if (strpos($headers, '200') === false) {
    http_response_code(400);
    exit;
}

// Parse JSON response body
$resp = json_decode($body, true);
if (!is_array($resp) || !isset($resp['user'], $resp['password'], $resp['host'], $resp['port'], $resp['db'], $resp['only_db'])) {
    http_response_code(400);
    exit;
}

// Verify response data types (prevent injection)
if (!is_string($resp['user']) || !is_string($resp['password']) ||
    !is_string($resp['host']) || !is_int($resp['port']) ||
    !is_string($resp['db']) || !is_string($resp['only_db'])) {
    http_response_code(400);
    exit;
}

// Set secure session cookie parameters before session_start
session_set_cookie_params([
    'secure' => true,
    'httponly' => true,
    'samesite' => 'Lax',
]);

// Start the session using the SignonSession name
session_name('SignonSession');
session_start();

// Populate the phpMyAdmin signon session structure.
// This mirrors the structure expected by phpMyAdmin 5.2.x per the
// examples/signon-script.php in the phpMyAdmin tarball.
// See: https://docs.phpmyadmin.net/en/latest/setup.html#authentication-modes
$_SESSION['PMA_SQL_HISTORY'] = [];
$_SESSION['PMA_SYSTEMINFO'] = '';

// The main auth structure: array of connections to offer.
// Each key (e.g., 'server_0') is an entry in the servers config.
// For simplicity, we authenticate to the one server (index 1 in phpMyAdmin cfg).
$_SESSION['cfg'] = [];
$_SESSION['cfg']['Server'] = [];
$_SESSION['cfg']['Server'][1] = [
    'user' => $resp['user'],
    'password' => $resp['password'],
    'host' => $resp['host'],
    'port' => $resp['port'],
];

// Allow phpMyAdmin to apply 'only_db' restriction.
// This prevents the authenticated user from seeing or accessing databases
// outside the allowed scope.
$_SESSION['cfg']['Server'][1]['only_db'] = $resp['only_db'];

// Store the default database so the redirect lands on it
$_SESSION['cfg']['Server'][1]['db'] = $resp['db'];

// Signal that this session has been authenticated via signon
$_SESSION['PMA_Auth_provider'] = 'signon';

// Redirect to phpMyAdmin with the database pre-selected.
// urlencode ensures the DB name is safe in the Location header.
header('Location: /phpmyadmin/index.php?db=' . urlencode($resp['db']));
exit;
?>
