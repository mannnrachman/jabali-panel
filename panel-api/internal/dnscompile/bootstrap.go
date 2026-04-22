package dnscompile

import (
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// BootstrapRecords returns the default records created on new-domain
// provisioning. All flagged Managed=true so the UI can mark them
// read-only; ManagedBy is left NULL so the M6 email-disable cleanup
// (which scopes deletes by managed_by="m6") can't touch them.
//
// Record shape:
//
//	@     A     <server ipv4>
//	@     AAAA  <server ipv6>           (when configured)
//	mail  A     <server ipv4>           (MX target — must stay an A, RFC 2181 §10.3)
//	mail  AAAA  <server ipv6>
//	www   CNAME @                       (follows apex; no duplicate IP)
//	@     MX    mail                    (priority 10)
//	@     TXT   "v=spf1 mx ip4:<v4> [ip6:<v6>] ~all"
//	_dmarc TXT  "v=DMARC1; p=none"
//
// Notes:
//   - www is a CNAME to the apex so an apex-IP change propagates without
//     a per-record rewrite. mail deliberately stays an A because MX
//     targets MUST NOT be CNAME aliases (RFC 2181 §10.3) — some mail
//     servers reject such zones with SERVFAIL.
//   - SPF includes explicit ip4/ip6 beyond the `mx` directive so mail
//     sent from the apex IP (not just from the mail host's A record)
//     still passes SPF checks — e.g. panel-local scripts sending via
//     the local stalwart over the apex bind.
func BootstrapRecords(zoneID, zoneName string, srv *models.ServerSettings, idNew func() string) []models.DNSRecord {
	now := time.Now().UTC()
	mk := func(name, typ, content string, priority int) models.DNSRecord {
		return models.DNSRecord{
			ID:        idNew(),
			ZoneID:    zoneID,
			Name:      name,
			Type:      typ,
			Content:   content,
			TTL:       3600,
			Priority:  priority,
			Managed:   true,
			IsEnabled: true,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	var out []models.DNSRecord
	if srv == nil {
		return out
	}

	// Apex + mail host IPs. www is added as a CNAME below.
	if srv.PublicIPv4 != "" {
		out = append(out, mk("@", "A", srv.PublicIPv4, 0))
		out = append(out, mk("mail", "A", srv.PublicIPv4, 0))
	}
	if srv.PublicIPv6 != "" {
		out = append(out, mk("@", "AAAA", srv.PublicIPv6, 0))
		out = append(out, mk("mail", "AAAA", srv.PublicIPv6, 0))
	}

	// www CNAME to the apex. Content is the zone FQDN verbatim (pdns
	// treats a no-dot content as fully-qualified — it does NOT append
	// the zone). Writing the zone name instead of a short label keeps
	// the reconciler's wire-to-pdns pass a pure passthrough — Compile
	// doesn't expand CNAME content today and adding that expansion
	// would implicitly rewrite operator-edited CNAMEs too.
	if zoneName != "" {
		out = append(out, mk("www", "CNAME", zoneName, 0))
	}

	// MX target is the short label "mail"; pdns stores+serves it verbatim.
	// This record is paired with the mail A/AAAA above.
	out = append(out, mk("@", "MX", "mail", 10))

	// SPF — always has `mx` (covers the mail.<domain> host via its A
	// record), plus explicit ip4/ip6 for the apex IP so panel-local
	// senders passing through stalwart at the apex bind still match.
	var spf strings.Builder
	spf.WriteString(`"v=spf1 mx`)
	if srv.PublicIPv4 != "" {
		spf.WriteString(" ip4:")
		spf.WriteString(srv.PublicIPv4)
	}
	if srv.PublicIPv6 != "" {
		spf.WriteString(" ip6:")
		spf.WriteString(srv.PublicIPv6)
	}
	spf.WriteString(` ~all"`)
	out = append(out, mk("@", "TXT", spf.String(), 0))

	out = append(out, mk("_dmarc", "TXT", `"v=DMARC1; p=none"`, 0))
	return out
}
