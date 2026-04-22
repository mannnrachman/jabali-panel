package dnscompile

import (
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// EmailRecordsManagedBy is the marker stamped into dns_records.managed_by
// for every record this file emits. The disable path uses it as the
// WHERE clause to scope cleanup — M4 bootstrap records (A / MX / SPF /
// DMARC) get NULL at create time via BootstrapRecords, so the M6
// delete-on-disable query can't accidentally touch them.
const EmailRecordsManagedBy = "m6"

// EmailRecordsSelector is the hardcoded DKIM selector used for v1.
// ADR-0043 carries a selector column on domains for future rotation;
// until we ship rotation, every domain uses the same "jabali" label.
const EmailRecordsSelector = "jabali"

// BuildEmailRecords returns the per-domain DNS records inserted on
// domain.email_enable beyond what M4's BootstrapRecords already put
// in place at domain-create time.
//
// What M4 already installs (via BootstrapRecords, flagged Managed=true
// but ManagedBy=NULL):
//
//	@       A    <server ipv4>
//	www     A    <server ipv4>
//	mail    A    <server ipv4>
//	@       MX   mail                        (priority 10)
//	@       TXT  "v=spf1 mx ~all"
//	_dmarc  TXT  "v=DMARC1; p=none"
//
// What M6 adds (flagged Managed=true, ManagedBy="m6"):
//
//	jabali._domainkey  TXT    "v=DKIM1; k=ed25519; p=<pubkey>"
//	autoconfig         CNAME  mail
//	_autodiscover._tcp SRV    0 0 443 mail
//
// Three records instead of the blueprint's "7 total" — the other four
// exist already and rewriting them would (a) invalidate any operator
// edit (the entire point of ManagedBy scoping is to preserve
// overrides) and (b) churn PowerDNS unnecessarily. If an install
// somehow has a domain with NO M4 bootstrap records (imported zone
// file, older panel version), the reconciler is the place to re-apply
// them — not the email-enable handler, which has a narrower remit.
//
// Contract notes:
//   - CNAME content is the FQDN "mail.<zone>". PowerDNS serves record
//     content verbatim — a short label "mail" would be sent as a
//     root-relative "mail." that clients can't resolve. Matches the
//     www-CNAME-to-apex convention established in BootstrapRecords.
//   - SRV content is "priority weight port target" per RFC 2782.
//     Target is the FQDN "mail.<zone>" for the same reason.
//   - TXT content is double-quoted to match BootstrapRecords' format so
//     a textual diff between the two won't flap on reconciliation.
func BuildEmailRecords(
	zoneID, zoneName, selector, dkimPublicKey string,
	idNew func() string,
	now time.Time,
) []models.DNSRecord {
	m6 := EmailRecordsManagedBy
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
			ManagedBy: &m6,
			IsEnabled: true,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	mailTarget := "mail." + zoneName
	return []models.DNSRecord{
		mk(selector+"._domainkey", "TXT", `"`+dkimPublicKey+`"`, 0),
		mk("autoconfig", "CNAME", mailTarget, 0),
		mk("_autodiscover._tcp", "SRV", "0 0 443 "+mailTarget, 0),
	}
}
