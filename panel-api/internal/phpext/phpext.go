package phpext

import (
	"fmt"
	"regexp"
	"sort"
)

// Spec describes one logical extension offered by the admin UI.
type Spec struct {
	// Name is the user-facing identifier (what the UI table shows).
	Name string
	// Packages is the list of apt package suffixes that provide this extension.
	// Empty for BuiltIn. Multiple packages means install/remove acts on all at once
	// (e.g. "xml" package maps to multiple extensions: dom, simplexml, xml, etc.).
	Packages []string
	// EnableName is the phpenmod/phpdismod module name. Usually == Name; for
	// bundled groups it's the underlying ini file's base name.
	EnableName string
	// BuiltIn is true for extensions bundled into php<v>-common / php<v>-cli.
	// Install/Remove actions are hidden for these; only Enable/Disable.
	BuiltIn bool
}

var allSpecs = []Spec{
	{Name: "apcu", Packages: []string{"apcu"}, EnableName: "apcu", BuiltIn: false},
	{Name: "bcmath", Packages: []string{"bcmath"}, EnableName: "bcmath", BuiltIn: false},
	{Name: "bz2", Packages: []string{"bz2"}, EnableName: "bz2", BuiltIn: false},
	{Name: "calendar", Packages: []string{}, EnableName: "calendar", BuiltIn: true},
	{Name: "ctype", Packages: []string{}, EnableName: "ctype", BuiltIn: true},
	{Name: "curl", Packages: []string{"curl"}, EnableName: "curl", BuiltIn: false},
	{Name: "dba", Packages: []string{"dba"}, EnableName: "dba", BuiltIn: false},
	{Name: "dom", Packages: []string{"xml"}, EnableName: "dom", BuiltIn: false},
	{Name: "enchant", Packages: []string{"enchant"}, EnableName: "enchant", BuiltIn: false},
	{Name: "exif", Packages: []string{}, EnableName: "exif", BuiltIn: true},
	{Name: "ffi", Packages: []string{}, EnableName: "ffi", BuiltIn: true},
	{Name: "fileinfo", Packages: []string{}, EnableName: "fileinfo", BuiltIn: true},
	{Name: "ftp", Packages: []string{}, EnableName: "ftp", BuiltIn: true},
	{Name: "gd", Packages: []string{"gd"}, EnableName: "gd", BuiltIn: false},
	{Name: "gettext", Packages: []string{}, EnableName: "gettext", BuiltIn: true},
	{Name: "gmp", Packages: []string{"gmp"}, EnableName: "gmp", BuiltIn: false},
	{Name: "gnupg", Packages: []string{"gnupg"}, EnableName: "gnupg", BuiltIn: false},
	{Name: "iconv", Packages: []string{}, EnableName: "iconv", BuiltIn: true},
	{Name: "igbinary", Packages: []string{"igbinary"}, EnableName: "igbinary", BuiltIn: false},
	{Name: "imagick", Packages: []string{"imagick"}, EnableName: "imagick", BuiltIn: false},
	{Name: "imap", Packages: []string{"imap"}, EnableName: "imap", BuiltIn: false},
	{Name: "intl", Packages: []string{"intl"}, EnableName: "intl", BuiltIn: false},
	{Name: "ldap", Packages: []string{"ldap"}, EnableName: "ldap", BuiltIn: false},
	{Name: "mailparse", Packages: []string{"mailparse"}, EnableName: "mailparse", BuiltIn: false},
	{Name: "mbstring", Packages: []string{"mbstring"}, EnableName: "mbstring", BuiltIn: false},
	{Name: "mcrypt", Packages: []string{"mcrypt"}, EnableName: "mcrypt", BuiltIn: false},
	{Name: "memcached", Packages: []string{"memcached"}, EnableName: "memcached", BuiltIn: false},
	{Name: "mongodb", Packages: []string{"mongodb"}, EnableName: "mongodb", BuiltIn: false},
	{Name: "msgpack", Packages: []string{"msgpack"}, EnableName: "msgpack", BuiltIn: false},
	{Name: "mysql", Packages: []string{"mysql"}, EnableName: "mysqli", BuiltIn: false},
	{Name: "mysqli", Packages: []string{"mysql"}, EnableName: "mysqli", BuiltIn: false},
	{Name: "mysqlnd", Packages: []string{}, EnableName: "mysqlnd", BuiltIn: true},
	{Name: "odbc", Packages: []string{"odbc"}, EnableName: "odbc", BuiltIn: false},
	{Name: "opcache", Packages: []string{}, EnableName: "opcache", BuiltIn: true},
	{Name: "pdo", Packages: []string{}, EnableName: "pdo", BuiltIn: true},
	{Name: "pdo_mysql", Packages: []string{"mysql"}, EnableName: "pdo_mysql", BuiltIn: false},
	{Name: "pdo_pgsql", Packages: []string{"pgsql"}, EnableName: "pdo_pgsql", BuiltIn: false},
	{Name: "pdo_sqlite", Packages: []string{"sqlite3"}, EnableName: "pdo_sqlite", BuiltIn: false},
	{Name: "pgsql", Packages: []string{"pgsql"}, EnableName: "pgsql", BuiltIn: false},
	{Name: "phar", Packages: []string{}, EnableName: "phar", BuiltIn: true},
	{Name: "posix", Packages: []string{}, EnableName: "posix", BuiltIn: true},
	{Name: "pspell", Packages: []string{"pspell"}, EnableName: "pspell", BuiltIn: false},
	{Name: "readline", Packages: []string{"readline"}, EnableName: "readline", BuiltIn: false},
	{Name: "redis", Packages: []string{"redis"}, EnableName: "redis", BuiltIn: false},
	{Name: "shmop", Packages: []string{}, EnableName: "shmop", BuiltIn: true},
	{Name: "simplexml", Packages: []string{"xml"}, EnableName: "simplexml", BuiltIn: false},
	{Name: "snmp", Packages: []string{"snmp"}, EnableName: "snmp", BuiltIn: false},
	{Name: "soap", Packages: []string{"soap"}, EnableName: "soap", BuiltIn: false},
	{Name: "sockets", Packages: []string{}, EnableName: "sockets", BuiltIn: true},
	{Name: "sqlite3", Packages: []string{"sqlite3"}, EnableName: "sqlite3", BuiltIn: false},
	{Name: "ssh2", Packages: []string{"ssh2"}, EnableName: "ssh2", BuiltIn: false},
	{Name: "sysvmsg", Packages: []string{}, EnableName: "sysvmsg", BuiltIn: true},
	{Name: "sysvsem", Packages: []string{}, EnableName: "sysvsem", BuiltIn: true},
	{Name: "sysvshm", Packages: []string{}, EnableName: "sysvshm", BuiltIn: true},
	{Name: "tidy", Packages: []string{"tidy"}, EnableName: "tidy", BuiltIn: false},
	{Name: "tokenizer", Packages: []string{}, EnableName: "tokenizer", BuiltIn: true},
	{Name: "xdebug", Packages: []string{"xdebug"}, EnableName: "xdebug", BuiltIn: false},
	{Name: "xml", Packages: []string{"xml"}, EnableName: "xml", BuiltIn: false},
	{Name: "xmlreader", Packages: []string{"xml"}, EnableName: "xmlreader", BuiltIn: false},
	{Name: "xmlwriter", Packages: []string{"xml"}, EnableName: "xmlwriter", BuiltIn: false},
	{Name: "xsl", Packages: []string{"xsl"}, EnableName: "xsl", BuiltIn: false},
	{Name: "yaml", Packages: []string{"yaml"}, EnableName: "yaml", BuiltIn: false},
	{Name: "zip", Packages: []string{"zip"}, EnableName: "zip", BuiltIn: false},
}

var allSpecsMap = func() map[string]Spec {
	m := make(map[string]Spec, len(allSpecs))
	for _, s := range allSpecs {
		m[s.Name] = s
	}
	return m
}()

var versionRegex = regexp.MustCompile(`^\d+\.\d+$`)

// All returns the full allowlist in alphabetical order.
func All() []Spec {
	result := make([]Spec, len(allSpecs))
	copy(result, allSpecs)
	return result
}

// Lookup returns the Spec for name, or ok=false if not in the allowlist.
func Lookup(name string) (Spec, bool) {
	s, ok := allSpecsMap[name]
	return s, ok
}

// ValidVersion reports whether v matches ^\d+\.\d+$.
func ValidVersion(v string) bool {
	return versionRegex.MatchString(v)
}

// ResolvePackages returns the concrete apt package names for (version, ext),
// e.g. ("8.5","curl") → ["php8.5-curl"], ("8.5","mysql") → ["php8.5-mysql"].
// Returns an error if ext is not in the allowlist or if version fails validation.
// Built-ins return (nil, nil).
func ResolvePackages(version, ext string) ([]string, error) {
	if !ValidVersion(version) {
		return nil, fmt.Errorf("phpext: invalid version %q", version)
	}

	spec, ok := Lookup(ext)
	if !ok {
		return nil, fmt.Errorf("phpext: unknown extension %q", ext)
	}

	if spec.BuiltIn {
		return nil, nil
	}

	if len(spec.Packages) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool)
	var result []string
	for _, pkg := range spec.Packages {
		pkgName := fmt.Sprintf("php%s-%s", version, pkg)
		if !seen[pkgName] {
			seen[pkgName] = true
			result = append(result, pkgName)
		}
	}

	sort.Strings(result)
	return result, nil
}
