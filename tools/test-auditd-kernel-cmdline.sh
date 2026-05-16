#!/usr/bin/env bash
# Regression: auditd needs audit=1 on the kernel cmdline.
#
# Bug (live mx.jabali-panel.local 2026-05-16): host booted without
# audit=1; systemd (PID 1) held the audit netlink, so the kernel routed
# syscall records to systemd/journald, not auditd. auditd started, loaded
# 33 rules, reported `enabled 1` — yet ZERO syscall audit events of any
# kind (fresh -w watch silent, no never/exclude rules). M39's exec-audit
# (jabali_susp_exec/web_exec/bin_tamper) was therefore inert.
#
# Contract: install_audit_exec ensures audit=1 + audit_backlog_limit on
# GRUB_CMDLINE_LINUX_DEFAULT (idempotent, reboot-gated via sentinel),
# mirroring the established AppArmor apparmor=1 GRUB pattern.
set -euo pipefail
R="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"; I="$R/install.sh"
fail(){ echo "FAIL: $*" >&2; exit 1; }
pass(){ echo "PASS: $*"; }

body="$(awk '/^install_audit_exec\(\)/,/^}/' "$I")"
grep -q 'audit=1 audit_backlog_limit=8192' <<<"$body" \
  || fail "install_audit_exec does not add 'audit=1 audit_backlog_limit=8192' to GRUB"
grep -q "grep -qE 'audit=1' /etc/default/grub" <<<"$body" \
  || fail "install_audit_exec audit=1 grub add is not idempotency-guarded"
grep -q '/etc/jabali/.audit-grub-pending' <<<"$body" \
  || fail "install_audit_exec missing reboot sentinel /etc/jabali/.audit-grub-pending"
grep -q 'update-grub' <<<"$body" \
  || fail "install_audit_exec does not run update-grub after editing cmdline"
pass "install_audit_exec ensures audit=1 kernel cmdline (idempotent + sentinel + update-grub)"
echo "ALL PASS: auditd kernel-cmdline contract holds"
