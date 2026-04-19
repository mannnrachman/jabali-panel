package phpext

import (
	"sort"
	"testing"
)

// TestAll_Length verifies the allowlist contains the exact number of extensions.
func TestAll_Length(t *testing.T) {
	all := All()
	const expected = 63
	if len(all) != expected {
		t.Errorf("expected %d extensions, got %d", expected, len(all))
	}
}

// TestAll_Alphabetical verifies extensions are sorted in strict alphabetical order.
func TestAll_Alphabetical(t *testing.T) {
	all := All()
	sorted := make([]Spec, len(all))
	copy(sorted, all)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for i, spec := range all {
		if spec.Name != sorted[i].Name {
			t.Errorf("extension at index %d is %q, expected %q (not in alphabetical order)", i, spec.Name, sorted[i].Name)
		}
	}
}

// TestAll_NoDuplicates verifies each extension name appears exactly once.
func TestAll_NoDuplicates(t *testing.T) {
	all := All()
	seen := make(map[string]bool)
	for _, spec := range all {
		if seen[spec.Name] {
			t.Errorf("duplicate extension: %q", spec.Name)
		}
		seen[spec.Name] = true
	}
}

// TestAll_ImmutableCopy verifies All() returns independent copies on each call.
func TestAll_ImmutableCopy(t *testing.T) {
	all1 := All()
	all2 := All()

	if &all1[0] == &all2[0] {
		t.Error("All() returned same underlying array, expected independent copies")
	}
}

// TestLookup_HitAndMiss verifies Lookup works for every extension in the allowlist.
func TestLookup_HitAndMiss(t *testing.T) {
	all := All()

	// Every extension in All() must be findable via Lookup
	for _, spec := range all {
		looked, ok := Lookup(spec.Name)
		if !ok {
			t.Errorf("Lookup(%q) returned ok=false, expected ok=true", spec.Name)
		}
		if looked.Name != spec.Name || looked.EnableName != spec.EnableName || looked.BuiltIn != spec.BuiltIn {
			t.Errorf("Lookup(%q) returned mismatched spec", spec.Name)
		}
		if len(looked.Packages) != len(spec.Packages) {
			t.Errorf("Lookup(%q) Packages mismatch: got %v, expected %v", spec.Name, looked.Packages, spec.Packages)
		}
		for i, pkg := range looked.Packages {
			if i >= len(spec.Packages) || pkg != spec.Packages[i] {
				t.Errorf("Lookup(%q) Packages mismatch: got %v, expected %v", spec.Name, looked.Packages, spec.Packages)
				break
			}
		}
	}

	// Unknown extensions must return ok=false
	if _, ok := Lookup("nonexistent"); ok {
		t.Error("Lookup(\"nonexistent\") returned ok=true, expected ok=false")
	}
}

// TestResolvePackages_Direct tests simple single-package resolution.
func TestResolvePackages_Direct(t *testing.T) {
	packages, err := ResolvePackages("8.5", "curl")
	if err != nil {
		t.Fatalf("ResolvePackages(\"8.5\", \"curl\") failed: %v", err)
	}
	if len(packages) != 1 || packages[0] != "php8.5-curl" {
		t.Errorf("expected [\"php8.5-curl\"], got %v", packages)
	}
}

// TestResolvePackages_BundledXML tests deduplication of bundled xml family.
func TestResolvePackages_BundledXML(t *testing.T) {
	// The "xml" package provides dom, simplexml, xml, xmlreader, xmlwriter
	packages, err := ResolvePackages("8.5", "xmlreader")
	if err != nil {
		t.Fatalf("ResolvePackages(\"8.5\", \"xmlreader\") failed: %v", err)
	}
	// All xml-family extensions map to the single "xml" package
	if len(packages) != 1 || packages[0] != "php8.5-xml" {
		t.Errorf("expected [\"php8.5-xml\"], got %v", packages)
	}
}

// TestResolvePackages_BuiltIn verifies built-in extensions return (nil, nil).
func TestResolvePackages_BuiltIn(t *testing.T) {
	tests := []string{"calendar", "ctype", "exif", "ffi", "fileinfo", "ftp", "gettext",
		"iconv", "mysqlnd", "opcache", "pdo", "phar", "posix", "shmop", "sockets",
		"sysvmsg", "sysvsem", "sysvshm", "tokenizer"}

	for _, ext := range tests {
		packages, err := ResolvePackages("8.5", ext)
		if err != nil {
			t.Errorf("ResolvePackages(\"8.5\", %q) failed: %v", ext, err)
		}
		if packages != nil {
			t.Errorf("ResolvePackages(\"8.5\", %q) returned non-nil packages %v, expected nil", ext, packages)
		}
	}
}

// TestResolvePackages_MysqlAlias verifies mysql meta-install maps to mysqli package.
func TestResolvePackages_MysqlAlias(t *testing.T) {
	// "mysql" and "mysqli" both map to the "mysql" apt package
	packagesAlias, err := ResolvePackages("8.5", "mysql")
	if err != nil {
		t.Fatalf("ResolvePackages(\"8.5\", \"mysql\") failed: %v", err)
	}
	packagesReal, err := ResolvePackages("8.5", "mysqli")
	if err != nil {
		t.Fatalf("ResolvePackages(\"8.5\", \"mysqli\") failed: %v", err)
	}

	if len(packagesAlias) != 1 || packagesAlias[0] != "php8.5-mysql" {
		t.Errorf("mysql alias: expected [\"php8.5-mysql\"], got %v", packagesAlias)
	}
	if len(packagesReal) != 1 || packagesReal[0] != "php8.5-mysql" {
		t.Errorf("mysqli real: expected [\"php8.5-mysql\"], got %v", packagesReal)
	}
}

// TestResolvePackages_BadVersion tests rejection of invalid version formats.
func TestResolvePackages_BadVersion(t *testing.T) {
	tests := []string{"8", "8.5.1", "abc", "", "8.x", "8_5"}

	for _, version := range tests {
		_, err := ResolvePackages(version, "curl")
		if err == nil {
			t.Errorf("ResolvePackages(%q, \"curl\") expected error, got nil", version)
		}
	}
}

// TestResolvePackages_UnknownExt tests rejection of unknown extensions.
func TestResolvePackages_UnknownExt(t *testing.T) {
	_, err := ResolvePackages("8.5", "nonexistent")
	if err == nil {
		t.Error("ResolvePackages(\"8.5\", \"nonexistent\") expected error, got nil")
	}
}

// TestValidVersion tests the version format validation regex.
func TestValidVersion(t *testing.T) {
	tests := []struct {
		version string
		valid   bool
	}{
		{"8.5", true},
		{"7.4", true},
		{"8.0", true},
		{"8", false},
		{"8.5.1", false},
		{"abc", false},
		{"", false},
		{"8.x", false},
		{"8_5", false},
		{".8", false},
		{"8.", false},
	}

	for _, tc := range tests {
		got := ValidVersion(tc.version)
		if got != tc.valid {
			t.Errorf("ValidVersion(%q) = %v, expected %v", tc.version, got, tc.valid)
		}
	}
}

// TestResolvePackages_Sorting verifies output packages are sorted alphabetically.
func TestResolvePackages_Sorting(t *testing.T) {
	packages, err := ResolvePackages("8.5", "imagick")
	if err != nil {
		t.Fatalf("ResolvePackages failed: %v", err)
	}

	// Check that packages are sorted
	sorted := make([]string, len(packages))
	copy(sorted, packages)
	sort.Strings(sorted)

	for i, pkg := range packages {
		if pkg != sorted[i] {
			t.Errorf("packages not sorted: got %v, expected %v", packages, sorted)
			break
		}
	}
}
