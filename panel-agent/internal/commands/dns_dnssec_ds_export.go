package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"git.linux-hosting.co.il/shukivaknin/jabali2/agentwire"
	"git.linux-hosting.co.il/shukivaknin/jabali2/panel-agent/internal/pdns"
)

type dnsDNSSECDSExportParams struct {
	DomainName string `json:"domain_name"`
}

type dnsDNSSECDSRecord struct {
	KeyTag     int    `json:"key_tag"`
	Algorithm  uint8  `json:"algorithm"`
	DigestType uint8  `json:"digest_type"`
	Digest     string `json:"digest"`
}

type dnsDNSSECDSExportResponse struct {
	DSRecords []dnsDNSSECDSRecord `json:"ds_records"`
}

func dnsDNSSECDSExportHandler(ctx context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "params required"}
	}
	var p dnsDNSSECDSExportParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: fmt.Sprintf("parse params: %v", err)}
	}
	if p.DomainName == "" {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInvalidArgument, Message: "domain_name required"}
	}
	records, err := pdns.ExportZoneDS(ctx, p.DomainName)
	if err != nil {
		return nil, &agentwire.AgentError{Code: agentwire.CodeInternal, Message: err.Error()}
	}
	out := make([]dnsDNSSECDSRecord, 0, len(records))
	for _, r := range records {
		out = append(out, dnsDNSSECDSRecord{
			KeyTag:     r.KeyTag,
			Algorithm:  r.Algorithm,
			DigestType: r.DigestType,
			Digest:     r.Digest,
		})
	}
	return dnsDNSSECDSExportResponse{DSRecords: out}, nil
}

func init() {
	Default.Register("dns.dnssec_ds_export", dnsDNSSECDSExportHandler)
}
