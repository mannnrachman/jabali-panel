package pdnsrecursor

import "testing"

func TestNSSetsEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both empty", nil, nil, true},
		{"identical", []string{"ns1.local", "ns2.local"}, []string{"ns1.local", "ns2.local"}, true},
		{"different length", []string{"ns1.local"}, []string{"ns1.local", "ns2.local"}, false},
		{"different content", []string{"ns1.local"}, []string{"ns1.public"}, false},
		// The canonical bug: recursor recursed to public, returned real-world
		// NS. Auth returned local NS. These must compare unequal.
		{"local vs public", []string{"ns1.jabali-panel.local"}, []string{"ns2.aspcloudhost.com"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nsSetsEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("nsSetsEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
