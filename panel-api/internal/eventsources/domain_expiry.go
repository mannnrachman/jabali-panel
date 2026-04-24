package eventsources

// Registrar-level domain expiry (not SSL cert expiry — that lives in
// cert_renew.go). Jabali doesn't currently store registrar renewal
// dates on the domains table because we never act as registrar; the
// information would need to come from a WHOIS/RDAP lookup.
//
// TODO(M15): add a scheduled WHOIS fetch that populates a new
// `domains.registrar_expires_at` column on a daily cadence, then
// replace this stub with a query + fire loop modelled on cert_renew.go.
