-- M6 Step 8 Phase B: per-mailbox webmail SSO.
--
-- Adds two things:
--
--   1. `mailboxes.password_enc` — plaintext-at-rest ciphertext encrypted
--      by /etc/jabali-panel/sso.key using the same AES-256-GCM envelope
--      as users.mysqladmin_password_enc. Populated on mailbox create +
--      on every password rotate so the panel can mint a Bulwark session
--      on the user's behalf without storing bcrypt-reversible material.
--
--   2. `mailbox_sso_tokens` — short-lived (5 min) single-use tokens
--      minted by POST /mailboxes/:id/sso. Mirror of phpmyadmin_sso_tokens
--      (migration 000027); the SHA-256 hash of the plaintext is stored
--      so a token DB leak doesn't hand out webmail sessions.
--
-- Existing mailboxes (pre-Step 8) have password_enc = NULL. The SSO
-- mint endpoint returns a typed error for those rows (UI shows a
-- "rotate password to enable webmail SSO" hint); the next rotate
-- repopulates it and unlocks the button.

ALTER TABLE mailboxes
  ADD COLUMN password_enc VARBINARY(512) NULL AFTER password_hash;

CREATE TABLE mailbox_sso_tokens (
  id CHAR(26) NOT NULL PRIMARY KEY,
  mailbox_id CHAR(26) NOT NULL,
  user_id CHAR(26) NOT NULL,
  token_hash CHAR(64) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  UNIQUE KEY uniq_mailbox_sso_token_hash (token_hash),
  INDEX idx_mailbox_sso_expires_at (expires_at),
  INDEX idx_mailbox_sso_mailbox_id (mailbox_id),
  FOREIGN KEY fk_mailbox_sso_mailbox (mailbox_id) REFERENCES mailboxes(id) ON DELETE CASCADE,
  FOREIGN KEY fk_mailbox_sso_user (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
