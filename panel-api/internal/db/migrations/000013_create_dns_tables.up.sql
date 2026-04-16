-- Panel-owned DNS declarative schema.
--
-- Architecturally this mirrors the nginx vhost flow: the panel DB is
-- source of truth, the reconciler compiles to PowerDNS's native SQL
-- backend (separate DB: jabali_pdns) via the agent. PowerDNS's own
-- tables are its runtime cache; these tables are the truth.
--
-- One row in dns_zones per hosted zone (one-to-one with a hosted domain
-- today, but decoupled so a zone can survive transient domain-row
-- changes). dns_records holds the individual resource records; SOA and
-- NS are panel-managed (not user-editable) but still stored here so the
-- reconciler can generate them deterministically.

CREATE TABLE dns_zones (
    id              CHAR(26)     NOT NULL,
    domain_id       CHAR(26)     NOT NULL,
    -- Apex name is copied from domains.name at creation time; stored
    -- here so DNS logic doesn't have to join on every read.
    name            VARCHAR(255) NOT NULL,
    -- Serial follows the "next change bumps it" rule; starts at
    -- YYYYMMDD01 for cosmetics. Agent bumps on every zone push.
    serial          BIGINT       NOT NULL DEFAULT 0,
    -- SOA minimums — operator-tunable later; safe defaults for now.
    refresh_seconds INT          NOT NULL DEFAULT 3600,
    retry_seconds   INT          NOT NULL DEFAULT 600,
    expire_seconds  INT          NOT NULL DEFAULT 604800,
    minimum_ttl     INT          NOT NULL DEFAULT 3600,
    -- Disabled zones stay in the DB but the reconciler skips the push.
    is_enabled      TINYINT(1)   NOT NULL DEFAULT 1,
    created_at      DATETIME(6)  NOT NULL,
    updated_at      DATETIME(6)  NOT NULL,

    PRIMARY KEY (id),
    UNIQUE KEY ux_dns_zones_name (name),
    UNIQUE KEY ux_dns_zones_domain_id (domain_id),
    CONSTRAINT fk_dns_zones_domain FOREIGN KEY (domain_id)
        REFERENCES domains (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE dns_records (
    id           CHAR(26)       NOT NULL,
    zone_id      CHAR(26)       NOT NULL,
    -- Name is the record name as the operator enters it: "@" for apex,
    -- "www", "mail", or a full FQDN. Agent expands @ to the zone name
    -- when writing to PowerDNS.
    name         VARCHAR(255)   NOT NULL,
    -- Type: A, AAAA, CNAME, MX, TXT, NS, SOA, CAA, SRV, PTR, SPF.
    type         VARCHAR(16)    NOT NULL,
    content      VARCHAR(4096)  NOT NULL,
    ttl          INT            NOT NULL DEFAULT 3600,
    -- MX/SRV priority. 0 for types that don't use it.
    priority     INT            NOT NULL DEFAULT 0,
    -- Managed = panel-owned (SOA/NS/default A records); users can't
    -- delete or edit these from the UI. Flipping to 0 lets an operator
    -- override in edge cases.
    managed      TINYINT(1)     NOT NULL DEFAULT 0,
    -- Disabled records stay in the DB but aren't served by pdns.
    is_enabled   TINYINT(1)     NOT NULL DEFAULT 1,
    created_at   DATETIME(6)    NOT NULL,
    updated_at   DATETIME(6)    NOT NULL,

    PRIMARY KEY (id),
    KEY ix_dns_records_zone_id (zone_id),
    KEY ix_dns_records_zone_name_type (zone_id, name, type),
    CONSTRAINT fk_dns_records_zone FOREIGN KEY (zone_id)
        REFERENCES dns_zones (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
