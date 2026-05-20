package stalwartadmin

// MtaOutboundThrottle wire shape pinned via live spike on .150
// (project_stalwart_mtaouthound_throttle_pin). The full schema:
//
//	{
//	  "description": "<short label>",
//	  "enable": true,
//	  "key": {"senderDomain": true, "sender": true, ...},   // MAP, not array
//	  "rate": {"count": <N>, "period": <ms>},                // period is MILLISECONDS
//	  "match": {"match": {}, "else": "true"}                 // simple always-fire
//	}
//
// Constants below mirror the MtaOutboundThrottleKey enum so callers
// pick the right axis without re-introducing string literals.
const (
	ThrottleKeyMX           = "mx"
	ThrottleKeyRemoteIP     = "remoteIp"
	ThrottleKeyLocalIP      = "localIp"
	ThrottleKeySender       = "sender"
	ThrottleKeySenderDomain = "senderDomain"
	ThrottleKeyRcptDomain   = "rcptDomain"
)

// MtaThrottleRate is the bucket. Period in MILLISECONDS (3600000 = 1h).
type MtaThrottleRate struct {
	Count  uint64 `json:"count"`
	Period uint64 `json:"period"`
}

// MtaThrottleMatch is the always-fire Expression shape verified on .150.
// Stalwart's full Expression grammar is richer; we use the trivial form.
type MtaThrottleMatch struct {
	Match map[string]any `json:"match"`
	Else  string         `json:"else"`
}

// MtaOutboundThrottlePayload is the JSON body Create / Update expect.
type MtaOutboundThrottlePayload struct {
	Description string           `json:"description"`
	Enable      bool             `json:"enable"`
	Key         map[string]bool  `json:"key"`
	Rate        MtaThrottleRate  `json:"rate"`
	Match       MtaThrottleMatch `json:"match"`
}

// NewAlwaysFireMatch is the "no condition — apply to every message"
// match block. Used by global-scope throttles.
func NewAlwaysFireMatch() MtaThrottleMatch {
	return MtaThrottleMatch{
		Match: map[string]any{},
		Else:  "true",
	}
}

// matchRule mirrors the per-key rule entry inside Stalwart's
// Expression `match` object: {"if": <bool-expr>, "then": <value>}.
// Pinned via live spike on .150 against MtaStageData
// (`{"match":{"0":{"if":"local_port == 25","then":"true"}},"else":"false"}`)
// and re-verified by create->get round-trip on MtaOutboundThrottle.
type matchRule struct {
	If   string `json:"if"`
	Then string `json:"then"`
}

// NewSenderFilterMatch fires the throttle ONLY when sender equals the
// given email address. Drives Wave 3 v2 per-user throttles:
// scope=user, scope_ref="<full email>".
//
// The single quotes around the address are part of Stalwart's
// expression literal syntax (verified on .150). The address is
// embedded verbatim - caller MUST sanitise it before building the
// throttle so an attacker can't inject `' || 1==1 || sender == '` and
// turn the throttle into always-fire.
func NewSenderFilterMatch(senderAddress string) MtaThrottleMatch {
	return MtaThrottleMatch{
		Match: map[string]any{
			"0": matchRule{
				If:   "sender == '" + senderAddress + "'",
				Then: "true",
			},
		},
		Else: "false",
	}
}

// NewSenderDomainFilterMatch fires the throttle ONLY when the sender's
// domain part equals the given domain. Drives Wave 3 v2 per-domain
// throttles: scope=domain, scope_ref="<domain>".
func NewSenderDomainFilterMatch(domain string) MtaThrottleMatch {
	return MtaThrottleMatch{
		Match: map[string]any{
			"0": matchRule{
				If:   "sender_domain == '" + domain + "'",
				Then: "true",
			},
		},
		Else: "false",
	}
}

// HourlyRate is shorthand for the common "N messages per hour" bucket.
func HourlyRate(count uint64) MtaThrottleRate {
	return MtaThrottleRate{Count: count, Period: 3600 * 1000}
}

// DailyRate is shorthand for "N messages per day".
func DailyRate(count uint64) MtaThrottleRate {
	return MtaThrottleRate{Count: count, Period: 86400 * 1000}
}
