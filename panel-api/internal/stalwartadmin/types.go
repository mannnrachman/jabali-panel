package stalwartadmin

import "time"

// DmarcExternalReport mirrors Stalwart's schema object of the same
// name (introspected via `stalwart-cli describe DmarcExternalReport`
// on .150 / Stalwart 1.0.0). Only the fields jabali persists / decides
// on are decoded — Stalwart adds richer per-record DKIM/SPF detail,
// but the panel collapses to the RFC 7489 Appendix-C row shape that
// matches mig 000139 `dmarc_aggregate`.
type DmarcExternalReport struct {
	ID         string     `json:"id"`
	From       string     `json:"from,omitempty"`
	To         []string   `json:"to,omitempty"`
	Subject    string     `json:"subject,omitempty"`
	ReceivedAt time.Time  `json:"receivedAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	Report     DmarcReport `json:"report"`
}

// DmarcReport is the inner RFC 7489 §A schema.
type DmarcReport struct {
	OrgName          string       `json:"orgName,omitempty"`
	Email            string       `json:"email,omitempty"`
	ReportID         string       `json:"reportId,omitempty"`
	Domain           string       `json:"domain"`
	DateRangeBegin   time.Time    `json:"dateRangeBegin"`
	DateRangeEnd     time.Time    `json:"dateRangeEnd"`
	Records          []DmarcRecord `json:"records"`
}

type DmarcRecord struct {
	SourceIP    string `json:"sourceIp"`
	Count       uint   `json:"count"`
	Disposition string `json:"disposition,omitempty"` // none|quarantine|reject (defaults to none)
	DKIMResult  string `json:"dkimResult,omitempty"`  // pass|fail (defaults to fail)
	SPFResult   string `json:"spfResult,omitempty"`
}

// TlsExternalReport mirrors Stalwart's TlsExternalReport schema. Same
// envelope as DMARC, different inner report payload (RFC 8460).
type TlsExternalReport struct {
	ID         string    `json:"id"`
	From       string    `json:"from,omitempty"`
	To         []string  `json:"to,omitempty"`
	Subject    string    `json:"subject,omitempty"`
	ReceivedAt time.Time `json:"receivedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Report     TlsReport `json:"report"`
}

type TlsReport struct {
	OrgName        string         `json:"orgName,omitempty"`
	ReportID       string         `json:"reportId,omitempty"`
	DateRangeBegin time.Time      `json:"dateRangeBegin"`
	DateRangeEnd   time.Time      `json:"dateRangeEnd"`
	Policies       []TlsPolicy    `json:"policies"`
}

type TlsPolicy struct {
	Domain         string             `json:"policyDomain"`
	PolicyType     string             `json:"policyType,omitempty"`     // sts|tlsa|no-policy-found
	TotalSuccessful uint              `json:"totalSuccessfulSessionCount"`
	TotalFailure    uint              `json:"totalFailureSessionCount"`
	FailureDetails  []TlsFailureDetail `json:"failureDetails,omitempty"`
}

type TlsFailureDetail struct {
	ResultType        string `json:"resultType"` // starttls-not-supported, etc
	SendingMxHostname string `json:"sendingMxHostname,omitempty"`
	FailureCount      uint   `json:"failureCount"`
}

// ArfExternalReport is Stalwart's parsed ARF (RFC 5965) feedback
// loop report — the "this user marked your message as spam" envelope
// Gmail/Microsoft/Yahoo send back when recipients click the spam
// button.
type ArfExternalReport struct {
	ID         string           `json:"id"`
	From       string           `json:"from,omitempty"`
	To         []string         `json:"to,omitempty"`
	Subject    string           `json:"subject,omitempty"`
	ReceivedAt time.Time        `json:"receivedAt"`
	ExpiresAt  time.Time        `json:"expiresAt"`
	Report     ArfFeedbackReport `json:"report"`
}

type ArfFeedbackReport struct {
	FeedbackType    string    `json:"feedbackType"` // abuse|fraud|virus|other|not-spam
	UserAgent       string    `json:"userAgent,omitempty"`
	ReportingMTA    string    `json:"reportingMta,omitempty"`
	OriginalRcptTo  string    `json:"originalRcptTo,omitempty"`
	SourceIP        string    `json:"sourceIp,omitempty"`
	IncidentsCount  uint      `json:"incidentsCount,omitempty"`
	ArrivalDate     time.Time `json:"arrivalDate,omitempty"`
	OriginalMailFrom string   `json:"originalMailFrom,omitempty"`
}
