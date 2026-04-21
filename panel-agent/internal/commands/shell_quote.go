package commands

import "strings"

// shellQuote wraps s in single quotes and escapes any embedded single
// quotes for safe use inside a bash -c "..." argument. Originally lived
// in grav_install.go before that app was removed; now shared by the
// remaining installers (opencart, phpbb, prestashop).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
