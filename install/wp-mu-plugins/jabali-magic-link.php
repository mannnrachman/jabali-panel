<?php
/**
 * Plugin Name: Jabali Magic Link
 * Description: Validates ?jabali_admin_login=<token> against the panel API and signs the operator into wp-admin. M22 — replaces the M16 OIDC plugin path.
 * Version: 1.0.0
 *
 * The two constants below are sed-substituted at install time by the
 * panel-agent (see panel-agent/internal/commands/wordpress_install.go's
 * installMagicLinkMUPlugin). The placeholders are kept namespaced so a
 * misfire of `sed` is obvious in `cat`.
 */

defined('JABALI_PANEL_HOST') || define('JABALI_PANEL_HOST', '__PANEL_HOST__');
defined('JABALI_INSTALL_ID') || define('JABALI_INSTALL_ID', '__INSTALL_ID__');

if (!defined('ABSPATH')) {
    exit;
}

add_action('wp_loaded', function () {
    // Pre-flight: token present?
    if (empty($_GET['jabali_admin_login'])) {
        return;
    }

    // ADR-0039 §11 — browser prefetch must NOT consume the token. Skip
    // any non-GET request and any GET with a prefetch hint.
    if (($_SERVER['REQUEST_METHOD'] ?? 'GET') !== 'GET') {
        return;
    }
    foreach (['HTTP_SEC_PURPOSE', 'HTTP_PURPOSE', 'HTTP_X_MOZ', 'HTTP_X_PURPOSE'] as $hdr) {
        $val = $_SERVER[$hdr] ?? '';
        if ($val !== '' && (stripos($val, 'prefetch') !== false || stripos($val, 'preview') !== false)) {
            return;
        }
    }

    $token = (string) $_GET['jabali_admin_login'];

    // ADR-0039 §9 — malformed input is a silent no-op so an attacker
    // can't DoS a public page by appending junk to the URL. The token
    // shape is `<22 base64url chars>.<43 base64url chars>` (16B id +
    // 32B HMAC, RawURLEncoding).
    if (!preg_match('/^[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}$/', $token)) {
        return;
    }

    // Configuration check. If install-time sed didn't run (placeholders
    // still present), no-op rather than die — operator will see the
    // panel UI button work nowhere and investigate.
    if (JABALI_PANEL_HOST === '__PANEL_HOST__' || JABALI_INSTALL_ID === '__INSTALL_ID__') {
        return;
    }

    $url = sprintf(
        'https://%s/applications/%s/magic-link/validate',
        JABALI_PANEL_HOST,
        rawurlencode(JABALI_INSTALL_ID)
    );

    $resp = wp_remote_post($url, [
        'timeout'     => 5,
        'sslverify'   => true,
        'blocking'    => true,
        'redirection' => 0,
        'headers'     => ['Content-Type' => 'application/json'],
        'body'        => wp_json_encode(['token' => $token]),
    ]);

    // ADR-0039 §1 — never let the URL leak via Referer to wp-admin's
    // third-party assets.
    header('Referrer-Policy: no-referrer');

    if (is_wp_error($resp)) {
        wp_die(
            esc_html('Magic-link validation failed: panel unreachable. Mint a fresh token from the panel and try again.'),
            'Login error',
            ['response' => 502]
        );
    }

    $code = (int) wp_remote_retrieve_response_code($resp);
    if ($code === 200) {
        $body = json_decode((string) wp_remote_retrieve_body($resp), true);
        $username = is_array($body) && isset($body['admin_user']) ? (string) $body['admin_user'] : '';
        $user = $username !== '' ? get_user_by('login', $username) : null;
        if (!$user) {
            wp_die(
                esc_html('Magic-link validated but the admin user does not exist on this install. Contact the panel operator.'),
                'Login error',
                ['response' => 500]
            );
        }
        wp_set_auth_cookie($user->ID, false, true);
        wp_safe_redirect(admin_url());
        exit;
    }

    // ADR-0039 §8 — never log the token or response body to PHP error_log.
    // Status code only is enough for the operator to triage.
    if ($code === 410) {
        wp_die(
            esc_html('Magic link has already been used or has expired. Return to the panel and click the button again.'),
            'Login link gone',
            ['response' => 410]
        );
    }
    if ($code === 401) {
        wp_die(esc_html('Magic link signature is invalid.'), 'Login error', ['response' => 401]);
    }
    if ($code === 429) {
        wp_die(
            esc_html('Magic link is in use by another login attempt. Wait a moment and try again.'),
            'Too busy',
            ['response' => 429]
        );
    }
    if ($code === 400) {
        wp_die(esc_html('Magic link is malformed.'), 'Login error', ['response' => 400]);
    }
    wp_die(
        esc_html(sprintf('Magic-link validation failed (panel returned HTTP %d).', $code)),
        'Login error',
        ['response' => $code]
    );
});
