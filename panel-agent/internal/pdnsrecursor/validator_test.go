package pdnsrecursor

import "testing"

func TestValidateZone(t *testing.T) {
	ok := []string{
		"example.com",
		"a.b.c",
		"panel.local",
		"x",
		"sub-domain.example-host.co.il",
		"123numbers.com",
	}
	for _, z := range ok {
		if err := validateZone(z); err != nil {
			t.Errorf("validateZone(%q) unexpected err: %v", z, err)
		}
	}
	bad := map[string]string{
		"":                    "empty",
		"example.com.":        "trailing dot",
		"EXAMPLE.COM":         "uppercase",
		"-starts-with-dash":   "leading dash",
		".starts-with-dot":    "leading dot",
		"ends-with-dash-":     "trailing dash",
		"has_underscore":      "underscore",
		"127.0.0.1":           "IP literal",
		"with spaces":         "whitespace",
	}
	for z, why := range bad {
		if err := validateZone(z); err == nil {
			t.Errorf("validateZone(%q) should fail (%s) but passed", z, why)
		}
	}
}

func TestValidateEntry(t *testing.T) {
	okEntries := []Entry{
		{Zone: "example.com", Addr: "127.0.0.1", Port: 5300},
		{Zone: "panel.local", Addr: "::1", Port: 5300},
	}
	for _, e := range okEntries {
		if err := validateEntry(e); err != nil {
			t.Errorf("validateEntry(%+v) unexpected err: %v", e, err)
		}
	}
	cases := []struct {
		name string
		e    Entry
	}{
		{"self-loop v4", Entry{"example.com", "127.0.0.1", 53}},
		{"self-loop v6", Entry{"example.com", "::1", 53}},
		{"non-loopback forwarder", Entry{"example.com", "8.8.8.8", 5300}},
		{"port zero", Entry{"example.com", "127.0.0.1", 0}},
		{"port too high", Entry{"example.com", "127.0.0.1", 70000}},
		{"empty addr", Entry{"example.com", "", 5300}},
		{"non-ip addr", Entry{"example.com", "notanip", 5300}},
		{"empty zone", Entry{"", "127.0.0.1", 5300}},
	}
	for _, c := range cases {
		if err := validateEntry(c.e); err == nil {
			t.Errorf("%s: validateEntry(%+v) should fail but passed", c.name, c.e)
		}
	}
}

func TestParseLine(t *testing.T) {
	cases := []struct {
		line    string
		want    Entry
		wantErr bool
	}{
		{"example.com=127.0.0.1:5300", Entry{"example.com", "127.0.0.1", 5300}, false},
		{"example.com=127.0.0.1", Entry{"example.com", "127.0.0.1", 5300}, false}, // default port
		{"panel.local=[::1]:5300", Entry{"panel.local", "::1", 5300}, false},
		{"panel.local=[::1]", Entry{"panel.local", "::1", 5300}, false},
		{"noequals", Entry{}, true},
		{"example.com=", Entry{}, true},
		{"example.com=127.0.0.1:notaport", Entry{}, true},
		{"example.com=127.0.0.1:53", Entry{}, true}, // self-loop on parse
		{"example.com=[unterminated", Entry{}, true},
	}
	for _, c := range cases {
		got, err := parseLine(c.line)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseLine(%q) should fail, got %+v", c.line, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseLine(%q) unexpected err: %v", c.line, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseLine(%q) = %+v, want %+v", c.line, got, c.want)
		}
	}
}

func TestEntryString(t *testing.T) {
	v4 := Entry{"example.com", "127.0.0.1", 5300}
	if got := v4.String(); got != "example.com=127.0.0.1:5300" {
		t.Errorf("v4 got %q", got)
	}
	v6 := Entry{"panel.local", "::1", 5300}
	if got := v6.String(); got != "panel.local=[::1]:5300" {
		t.Errorf("v6 got %q", got)
	}
}
