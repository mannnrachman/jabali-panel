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
// match block. Wave 3 uses this for every throttle; conditional
// matching (e.g. "only when sender domain == foo.com") needs the
// Stalwart Expression grammar pinned separately.
func NewAlwaysFireMatch() MtaThrottleMatch {
	return MtaThrottleMatch{
		Match: map[string]any{},
		Else:  "true",
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
