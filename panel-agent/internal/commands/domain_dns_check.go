package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
)

// domain.dns_check — M38 Ghost Domain Detector.
//
// Resolves the apex A/AAAA records for a domain via the system
// resolver (which runs against pdns-recursor on the loopback per
// ADR-0047) and reports them back to panel-api. panel-api compares
// against the expected set (managed_ips for the server) and writes
// the resulting state — ok / mismatch / nxdomain / error — onto the
// `domains.ghost_state` column. The agent stays out of policy: this
// command only resolves and returns; classification belongs server-
// side where managed_ips lives.
//
// We deliberately do NOT use a custom resolver pointed at 8.8.8.8.
// Operators sometimes run split-horizon DNS where the public answer
// differs from the host's view; the host's resolver is the right
// definition of "what does this server's nginx vhost actually see
// when a browser hits it from out there?" — public-source-of-truth
// reachability is downstream of that. If the host's pdns-recursor
// is misconfigured, the detector will incorrectly flag domains; the
// recursor's own /jabali-admin/dns health is the place to fix it.

type domainDNSCheckParams struct {
	DomainName string `json:"domain_name"`
}

type domainDNSCheckResponse struct {
	IPv4    []string `json:"ipv4"`
	IPv6    []string `json:"ipv6"`
	NXDOMAIN bool    `json:"nxdomain"`
	Detail  string   `json:"detail"` // human note for panel-api to surface
}

const domainDNSCheckTimeout = 5 * time.Second

func domainDNSCheckHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p domainDNSCheckParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.DomainName == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "domain_name required"}
	}

	resCtx, cancel := context.WithTimeout(ctx, domainDNSCheckTimeout)
	defer cancel()

	resolver := net.DefaultResolver
	addrs, err := resolver.LookupIPAddr(resCtx, p.DomainName)
	if err != nil {
		// Distinguish NXDOMAIN from transient errors so panel-api can
		// classify "ghost" vs "error" correctly.
		if dnsErr, ok := err.(*net.DNSError); ok && dnsErr.IsNotFound {
			return domainDNSCheckResponse{NXDOMAIN: true, Detail: "no A or AAAA record"}, nil
		}
		return domainDNSCheckResponse{Detail: trimErr(err)}, &agentwire.AgentError{
			Code:    agentwire.CodeInternal,
			Message: trimErr(err),
		}
	}

	out := domainDNSCheckResponse{}
	for _, a := range addrs {
		if a.IP.To4() != nil {
			out.IPv4 = append(out.IPv4, a.IP.String())
		} else {
			out.IPv6 = append(out.IPv6, a.IP.String())
		}
	}
	if len(out.IPv4) == 0 && len(out.IPv6) == 0 {
		out.NXDOMAIN = true
		out.Detail = "no A or AAAA record"
	}
	return out, nil
}

func trimErr(err error) string {
	s := err.Error()
	const max = 200
	if len(s) > max {
		s = s[:max] + "…"
	}
	return strings.TrimSpace(s)
}

func init() {
	Default.Register("domain.dns_check", domainDNSCheckHandler)
}
