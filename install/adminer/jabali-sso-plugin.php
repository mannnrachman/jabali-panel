<?php
/**
 * Jabali Adminer SSO plugin.
 *
 * Exchanges a single-use base64url token (URL `?token=`) for engine-
 * specific credentials by POSTing to /sso/adminer/validate over the
 * panel-api Unix domain socket. The socket is owned jabali:www-data
 * mode 0660 — Adminer runs in PHP-FPM as www-data, so it can stream
 * the request without touching the network stack.
 *
 * Token is consumed atomically server-side (FOR UPDATE + DELETE);
 * replay returns 404 and we hand the user to Adminer's normal login
 * form so they can see what failed.
 *
 * Driver mapping:
 *   - mariadb  → "server"  (Adminer's MySQLi/PDO_MySQL backend)
 *   - postgres → "pgsql"   (libpq via pg_connect)
 *
 * @see panel-api/internal/api/sso_adminer_validate.go
 */
class JabaliAdminerSSO {
    /** @var string Path to the panel-api Unix socket. */
    private $socket_path;
    /** @var array|null Validated credentials (lazy). */
    private $creds;

    public function __construct($socket_path) {
        $this->socket_path = $socket_path;
    }

    /**
     * Lazy fetch — called by every override below. Returns null on
     * any failure so Adminer falls through to its default form.
     */
    private function fetchCreds() {
        if ($this->creds !== null) return $this->creds === false ? null : $this->creds;
        $token = isset($_GET['token']) ? $_GET['token'] : '';
        if ($token === '' || !preg_match('/^[A-Za-z0-9_-]{40,200}$/', $token)) {
            $this->creds = false;
            return null;
        }
        $body = json_encode(['token' => $token]);
        $context = stream_context_create([
            'http' => [
                'method'  => 'POST',
                'header'  => "Host: jabali-sso\r\nContent-Type: application/json\r\nContent-Length: " . strlen($body) . "\r\n",
                'content' => $body,
                'timeout' => 5,
                'ignore_errors' => true,
            ],
        ]);
        $url = 'http://localhost/sso/adminer/validate';
        // PHP's http stream wrapper doesn't speak unix:// directly —
        // open the socket manually and shake hands by hand.
        $fp = @stream_socket_client('unix://' . $this->socket_path, $errno, $errstr, 5);
        if (!$fp) {
            $this->creds = false;
            return null;
        }
        $req = "POST /sso/adminer/validate HTTP/1.1\r\n"
             . "Host: jabali-sso\r\n"
             . "Content-Type: application/json\r\n"
             . "Content-Length: " . strlen($body) . "\r\n"
             . "Connection: close\r\n\r\n"
             . $body;
        fwrite($fp, $req);
        $resp = '';
        while (!feof($fp)) {
            $resp .= fread($fp, 4096);
        }
        fclose($fp);
        // Split headers / body on first blank line.
        $sep = strpos($resp, "\r\n\r\n");
        if ($sep === false) {
            $this->creds = false;
            return null;
        }
        $headers = substr($resp, 0, $sep);
        $payload = substr($resp, $sep + 4);
        // Status line check.
        if (!preg_match('#^HTTP/1\\.\\d\\s+200\\b#', $headers)) {
            $this->creds = false;
            return null;
        }
        // Strip transfer-encoding: chunked if present.
        if (stripos($headers, 'Transfer-Encoding: chunked') !== false) {
            $payload = $this->dechunk($payload);
        }
        $j = json_decode($payload, true);
        if (!is_array($j) || !isset($j['driver']) || !isset($j['username']) ||
            !isset($j['password']) || !isset($j['db'])) {
            $this->creds = false;
            return null;
        }
        $this->creds = $j;
        return $j;
    }

    private function dechunk($body) {
        $out = '';
        $i = 0;
        $len = strlen($body);
        while ($i < $len) {
            $crlf = strpos($body, "\r\n", $i);
            if ($crlf === false) break;
            $size = hexdec(substr($body, $i, $crlf - $i));
            $i = $crlf + 2;
            if ($size === 0) break;
            $out .= substr($body, $i, $size);
            $i += $size + 2;
        }
        return $out;
    }

    /** Override the credential prompt. Adminer calls this for every page. */
    public function credentials() {
        $c = $this->fetchCreds();
        if ($c === null) return null;
        return [$c['server'], $c['username'], $c['password']];
    }

    /** Force the engine driver. */
    public function loginForm() {
        // Returning truthy keeps Adminer's form behaviour. We instead
        // pre-fill via auth() below by setting $_POST values when the
        // SSO creds are valid; if invalid, Adminer falls through to
        // its standard form so the user sees the failure UI.
        $c = $this->fetchCreds();
        if ($c === null) return;
        // Minimal CSS placeholder; real flow auto-submits via login().
        echo '<p>Signing into Adminer via Jabali SSO…</p>';
    }

    /**
     * login() runs after Adminer's parent::login() validates form
     * data. Returning true here completes the auth round-trip.
     */
    public function login($login, $password) {
        $c = $this->fetchCreds();
        if ($c === null) return null;
        return ($login === $c['username'] && $password === $c['password']);
    }

    /** database() pins the visible DB to what was validated. */
    public function database() {
        $c = $this->fetchCreds();
        if ($c === null) return null;
        return $c['db'];
    }

    /** databases() restricts the dropdown to the validated DB only. */
    public function databases($flush = true) {
        $c = $this->fetchCreds();
        if ($c === null) return null;
        return [$c['db']];
    }

    /** Disable permanent-login cookies — every entry is fresh-token. */
    public function permanentLogin($create = false) {
        return false;
    }

    /**
     * Auto-injection: when Adminer renders the login form, populate
     * $_POST so it submits itself on the very first request.
     */
    public function head() {
        $c = $this->fetchCreds();
        if ($c === null) return;
        if (!isset($_POST['auth'])) {
            $_POST['auth'] = [
                'driver'   => $c['driver'],
                'server'   => $c['server'],
                'username' => $c['username'],
                'password' => $c['password'],
                'db'       => $c['db'],
            ];
        }
    }
}
