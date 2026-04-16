package dnscompile

import (
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-api/internal/models"
)

// BootstrapRecords returns the default records created on new-domain
// provisioning: apex A (+ AAAA if IPv6), www A, mail A, MX, baseline
// SPF, baseline DMARC. All flagged Managed=true so the UI can mark
// them read-only.
func BootstrapRecords(zoneID string, srv *models.ServerSettings, idNew func() string) []models.DNSRecord {
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
	if srv.PublicIPv4 != "" {
		out = append(out, mk("@", "A", srv.PublicIPv4, 0))
		out = append(out, mk("www", "A", srv.PublicIPv4, 0))
		out = append(out, mk("mail", "A", srv.PublicIPv4, 0))
	}
	if srv.PublicIPv6 != "" {
		out = append(out, mk("@", "AAAA", srv.PublicIPv6, 0))
		out = append(out, mk("www", "AAAA", srv.PublicIPv6, 0))
		out = append(out, mk("mail", "AAAA", srv.PublicIPv6, 0))
	}
	out = append(out, mk("@", "MX", "mail", 10))          // content is target host, priority separate
	out = append(out, mk("@", "TXT", `"v=spf1 mx ~all"`, 0))
	out = append(out, mk("_dmarc", "TXT", `"v=DMARC1; p=none"`, 0))
	return out
}
