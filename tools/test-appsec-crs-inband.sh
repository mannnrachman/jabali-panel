#!/usr/bin/env bash
# Regression: CrowdSec AppSec must provide generic LFI/SQLi/XSS coverage
# (CRS), not just CVE virtual-patches.
#
# Bug (live mx.jabali-panel.local 2026-05-16): jabali-appsec.yaml
# inband_rules = vpatch-* + generic-* only; install.sh actively
# sed-stripped crowdsecurity/crs-* AND crowdsecurity/base-config every
# pass (correct under the OLD hub layout: crs hub-data 500'd, base-config
# was an appsec-CONFIG). Both resolved upstream — base-config is now an
# appsec-RULE and crs installs cleanly. With them stripped, LFI/SQLi/XSS
# sailed through (HTTP 200); only /.env (vpatch-env-access) blocked.
#
# Contracts:
#   A1 install.sh no longer blindly sed-strips crs-*/base-config from
#      inband_rules (the old strip blocks are gone).
#   A2 install.sh best-effort-installs crowdsecurity/crs + base-config
#      + crs-exclusion-plugin-wordpress (|| _warn, never hard-fail).
#   A3 inband_rules is presence-gated against the FLAT appsec-rules dir
#      /etc/crowdsec/appsec-rules (symlinks; -f follows) — never the
#      non-existent crowdsecurity/ subdir — so crowdsec never references
#      a missing rule (startup crash) yet gains CRS when present.
set -euo pipefail
R="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"; I="$R/install.sh"
fail(){ echo "FAIL: $*" >&2; exit 1; }
pass(){ echo "PASS: $*"; }

grep -Eq "sed -i '/crowdsecurity..crs-../d'" "$I" \
  && fail "A1: install.sh still blind-strips crowdsecurity/crs-* from inband" || true
grep -Eq "sed -i '/crowdsecurity..base-config/d' \"\\\$config_file\"" "$I" \
  && fail "A1: install.sh still blind-strips crowdsecurity/base-config from inband" || true
pass "A1: blind crs-*/base-config strip removed"

for r in crs base-config crs-exclusion-plugin-wordpress; do
  # install + "|| _warn" are split across a line-continuation; check both.
  grep -Eq "cscli appsec-rules install crowdsecurity/$r 2>/dev/null" "$I" \
    || fail "A2: install.sh missing 'cscli appsec-rules install crowdsecurity/$r'"
done
grep -Eq '\|\| _warn "appsec crowdsecurity/crs install failed' "$I" \
  || fail "A2: crs install not guarded by || _warn (would hard-fail the installer)"
grep -Eq '\|\| _warn "appsec crs WordPress exclusion install failed' "$I" \
  || fail "A2: wp-exclusion install not guarded by || _warn"
pass "A2: crs/base-config/wp-exclusion best-effort installed"

grep -Eq 'local _ar="/etc/crowdsec/appsec-rules"' "$I" \
  || fail "A3: builder _ar not the flat /etc/crowdsec/appsec-rules path"
grep -Eq '\[\[ -f "\$_ar/crs.yaml" \]\] && _inband\+=\("crowdsecurity/crs"\)' "$I" \
  || fail "A3: inband not presence-gated on \$_ar/crs.yaml"
grep -Eq '\[\[ -f "\$_ar/base-config.yaml" \]\] && _inband\+=\("crowdsecurity/base-config"\)' "$I" \
  || fail "A3: inband not presence-gated on \$_ar/base-config.yaml"
pass "A3: inband_rules presence-gated against flat appsec-rules dir"

echo "ALL PASS: AppSec CRS inband contracts hold"
