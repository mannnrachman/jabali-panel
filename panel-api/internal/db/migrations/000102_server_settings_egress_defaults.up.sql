-- M34 Step 6: per-server default egress allowlist + burst threshold.
--
-- Operators can harden defaults beyond the agent's baked-in allowlist
-- (drop SMTP submission ports on a no-mail-from-PHP host) or add a
-- corporate CIDR like an internal monitoring service. Reconciler reads
-- these on every tick and forwards them to user.egress.apply.
--
-- NULL columns mean "use the agent's baked-in CanonicalDefaults()".
-- Empty arrays ([]) mean "no destinations of this kind allowed".
-- The distinction matters: operators who explicitly empty out the
-- TCP-port list want NO outbound TCP allowed, vs operators who never
-- touched the column want the full default set.
--
-- egress_burst_threshold is the M14 burst-source signal threshold —
-- drops/tick. 50 is the default and matches the figure the runbook
-- documents; operators can dial up/down without redeploying panel-api.

ALTER TABLE server_settings
  ADD COLUMN egress_default_loopback_cidrs JSON NULL AFTER vapid_private_key,
  ADD COLUMN egress_default_loopback6_cidrs JSON NULL AFTER egress_default_loopback_cidrs,
  ADD COLUMN egress_default_ports_tcp JSON NULL AFTER egress_default_loopback6_cidrs,
  ADD COLUMN egress_default_ports_udp JSON NULL AFTER egress_default_ports_tcp,
  ADD COLUMN egress_burst_threshold INT UNSIGNED NOT NULL DEFAULT 50 AFTER egress_default_ports_udp;
