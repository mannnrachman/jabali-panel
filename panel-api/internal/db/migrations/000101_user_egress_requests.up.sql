-- M34: per-user egress access-request queue.
--
-- A user with state in (learning, enforced) who needs an outbound
-- destination not in the global default allowlist submits a request.
-- Admin reviews via /admin/egress-requests; on approve, the destination
-- is added to user_egress_policies.allowed_extra and the next reconciler
-- tick re-renders the nftables table.
--
-- Status flow: pending -> approved | denied. No re-open path; the user
-- submits a new row if they want to retry. reviewed_by + decided_at
-- hold the audit metadata; created_at is preserved for queue ordering.

CREATE TABLE IF NOT EXISTS user_egress_requests (
  id           VARCHAR(26)  NOT NULL,
  user_id      VARCHAR(26)  NOT NULL,
  cidr         VARCHAR(43)  NOT NULL,
  port         INT UNSIGNED NULL,
  protocol     ENUM('tcp','udp') NOT NULL DEFAULT 'tcp',
  reason       VARCHAR(500) NOT NULL,
  status       ENUM('pending','approved','denied') NOT NULL DEFAULT 'pending',
  reviewed_by  VARCHAR(26)  NULL,
  decided_at   TIMESTAMP    NULL,
  created_at   TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  KEY idx_egress_req_user_status (user_id, status, created_at),
  KEY idx_egress_req_status_created (status, created_at),
  CONSTRAINT fk_egress_req_user FOREIGN KEY (user_id)
    REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
