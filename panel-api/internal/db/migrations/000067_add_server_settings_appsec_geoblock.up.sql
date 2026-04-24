-- M26 AppSec geoblock (server-wide). Governs the YAML rule at
-- /etc/crowdsec/appsec-rules/jabali-geoblock.yaml via the agent
-- command security.crowdsec.appsec.geoblock.set. DB is the source of
-- truth; agent rewrites the file + reloads crowdsec on every set.

ALTER TABLE server_settings
    ADD COLUMN appsec_geoblock_mode      VARCHAR(10)   NOT NULL DEFAULT 'off',
    ADD COLUMN appsec_geoblock_countries VARCHAR(1000) NOT NULL DEFAULT '';
