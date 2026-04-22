-- Drop must precede the FK-bearing column drop in 000058 if rolling
-- both back; `migrate down 2` runs them in reverse order automatically.
DROP TABLE IF EXISTS managed_ips;
