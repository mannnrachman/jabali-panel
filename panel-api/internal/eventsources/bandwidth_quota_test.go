package eventsources

import "testing"

// classifyQuota is the pure decision core. Test edge cases here so
// the threshold logic stays correct without standing up a Queue +
// History + BWDaily stub.

func TestClassifyQuota_NoQuota_NoEvent(t *testing.T) {
	total, pct, kind, sev := classifyQuota(map[string]uint64{"d": 9999999999}, 0)
	if kind != "" || sev != "" {
		t.Errorf("zero quota must short-circuit; got kind=%q sev=%q", kind, sev)
	}
	if total == 0 {
		t.Errorf("total must still be summed even when quota disabled; got 0")
	}
	if pct != 0 {
		t.Errorf("zero-quota: pct must be 0 (no division); got %v", pct)
	}
}

func TestClassifyQuota_UnderWarn_NoEvent(t *testing.T) {
	// 10 MB used / 100 MB quota = 10% — well under 80% warn floor.
	total, pct, kind, sev := classifyQuota(map[string]uint64{"d": 10 * 1024 * 1024}, 100)
	if kind != "" || sev != "" {
		t.Errorf("under warn must not fire; got kind=%q sev=%q", kind, sev)
	}
	if total != 10*1024*1024 {
		t.Errorf("total wrong: %d", total)
	}
	if pct < 9 || pct > 11 {
		t.Errorf("pct ~10%%, got %v", pct)
	}
}

func TestClassifyQuota_AtWarn_FiresWarn(t *testing.T) {
	// 80 MB used / 100 MB quota = 80% exactly — boundary fires warn.
	_, _, kind, sev := classifyQuota(map[string]uint64{"d": 80 * 1024 * 1024}, 100)
	if kind != "bandwidth.quota.warn" {
		t.Errorf("warn boundary: want kind=bandwidth.quota.warn, got %q", kind)
	}
	if sev != "warning" {
		t.Errorf("warn boundary: want sev=warning, got %q", sev)
	}
}

func TestClassifyQuota_AtCrit_FiresCrit(t *testing.T) {
	// 100 MB used / 100 MB quota = 100% exactly — boundary fires crit.
	_, _, kind, sev := classifyQuota(map[string]uint64{"d": 100 * 1024 * 1024}, 100)
	if kind != "bandwidth.quota.crit" {
		t.Errorf("crit boundary: want bandwidth.quota.crit, got %q", kind)
	}
	if sev != "critical" {
		t.Errorf("crit boundary: want sev=critical, got %q", sev)
	}
}

func TestClassifyQuota_OverCrit_StaysCrit(t *testing.T) {
	// 200% — still crit, never escalates beyond.
	_, pct, kind, _ := classifyQuota(map[string]uint64{"d": 200 * 1024 * 1024}, 100)
	if kind != "bandwidth.quota.crit" {
		t.Errorf("over-crit must stay crit; got %q", kind)
	}
	if pct < 199 || pct > 201 {
		t.Errorf("pct ~200, got %v", pct)
	}
}

func TestClassifyQuota_AggregatesAcrossDomains(t *testing.T) {
	// Three domains adding up to crit threshold.
	_, _, kind, _ := classifyQuota(
		map[string]uint64{
			"d1": 50 * 1024 * 1024,
			"d2": 30 * 1024 * 1024,
			"d3": 20 * 1024 * 1024,
		},
		100,
	)
	if kind != "bandwidth.quota.crit" {
		t.Errorf("multi-domain sum must hit crit; got %q", kind)
	}
}

func TestClassifyQuota_EmptyMap_NoEvent(t *testing.T) {
	total, pct, kind, _ := classifyQuota(map[string]uint64{}, 100)
	if kind != "" {
		t.Errorf("zero traffic must not fire; got %q", kind)
	}
	if total != 0 || pct != 0 {
		t.Errorf("empty: want total=0 pct=0, got total=%d pct=%v", total, pct)
	}
}

func TestClassifyQuota_NilMap_NoEvent(t *testing.T) {
	total, _, kind, _ := classifyQuota(nil, 100)
	if kind != "" {
		t.Errorf("nil map must not fire; got %q", kind)
	}
	if total != 0 {
		t.Errorf("nil: total must be 0; got %d", total)
	}
}
