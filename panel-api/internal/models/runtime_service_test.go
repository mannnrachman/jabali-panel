package models

import "testing"

func TestIsValidRuntimeType(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"php", true},
		{"nodejs", true},
		{"python", true},
		{"go", true},
		{"docker", true},
		{"static", true},
		{"", false},
		{"ruby", false},
		{"PHP", false},   // case-sensitive
		{"node", false},  // must be "nodejs"
		{"java", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := IsValidRuntimeType(tt.input); got != tt.want {
				t.Errorf("IsValidRuntimeType(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestRuntimeNeedsProxy(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"php", false},
		{"static", false},
		{"", false},
		{"nodejs", true},
		{"python", true},
		{"go", true},
		{"docker", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := RuntimeNeedsProxy(tt.input); got != tt.want {
				t.Errorf("RuntimeNeedsProxy(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEnvVarsValueScan(t *testing.T) {
	original := EnvVars{"NODE_ENV": "production", "PORT": "3000"}

	// Value round-trip
	val, err := original.Value()
	if err != nil {
		t.Fatalf("EnvVars.Value() error: %v", err)
	}
	if val == nil {
		t.Fatal("EnvVars.Value() returned nil for non-empty map")
	}

	// Scan from []byte
	var scanned EnvVars
	if err := scanned.Scan(val); err != nil {
		t.Fatalf("EnvVars.Scan([]byte) error: %v", err)
	}
	if scanned["NODE_ENV"] != "production" {
		t.Errorf("got NODE_ENV=%q, want %q", scanned["NODE_ENV"], "production")
	}
	if scanned["PORT"] != "3000" {
		t.Errorf("got PORT=%q, want %q", scanned["PORT"], "3000")
	}

	// Scan from string
	var scanned2 EnvVars
	if err := scanned2.Scan(string(val.([]byte))); err != nil {
		t.Fatalf("EnvVars.Scan(string) error: %v", err)
	}
	if scanned2["NODE_ENV"] != "production" {
		t.Errorf("string scan: got NODE_ENV=%q, want %q", scanned2["NODE_ENV"], "production")
	}
}

func TestEnvVarsNilHandling(t *testing.T) {
	// Empty map → nil
	empty := EnvVars{}
	val, err := empty.Value()
	if err != nil {
		t.Fatalf("empty EnvVars.Value() error: %v", err)
	}
	if val != nil {
		t.Errorf("empty EnvVars.Value() = %v, want nil", val)
	}

	// Scan nil → nil map
	var scanned EnvVars
	if err := scanned.Scan(nil); err != nil {
		t.Fatalf("EnvVars.Scan(nil) error: %v", err)
	}
	if scanned != nil {
		t.Errorf("EnvVars.Scan(nil) = %v, want nil", scanned)
	}
}
