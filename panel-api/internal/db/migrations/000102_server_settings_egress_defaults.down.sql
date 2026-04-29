ALTER TABLE server_settings
  DROP COLUMN egress_default_loopback_cidrs,
  DROP COLUMN egress_default_loopback6_cidrs,
  DROP COLUMN egress_default_ports_tcp,
  DROP COLUMN egress_default_ports_udp,
  DROP COLUMN egress_burst_threshold;
