-- M44: Automation API scoped tokens.
--
-- HMAC-signed bearer tokens external automations use to hit a small
-- read-only surface of /api/v1/automation/*. Per-token scopes
-- constrain the blast radius. Plaintext secret is returned exactly
-- once at mint time; thereafter only secret_enc (AES-GCM via ssokey)
-- lives on disk.
--
-- created_by is nullable so an admin user deletion doesn't cascade-
-- nuke automation tokens that pre-existed; the audit trail preserves
-- "who minted this" until the row itself is revoked + reaped.

CREATE TABLE automation_tokens (
    id            CHAR(26)        NOT NULL PRIMARY KEY,
    name          VARCHAR(100)    NOT NULL,
    scopes_json   JSON            NOT NULL,
    secret_enc    VARBINARY(255)  NOT NULL,
    created_by    CHAR(26)        NULL,
    created_at    DATETIME(6)     NOT NULL,
    last_used_at  DATETIME(6)     NULL,
    last_used_ip  VARCHAR(45)     NULL,
    revoked_at    DATETIME(6)     NULL,
    UNIQUE KEY uq_automation_tokens_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE INDEX idx_automation_tokens_revoked ON automation_tokens (revoked_at);
