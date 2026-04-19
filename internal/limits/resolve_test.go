package limits

import (
	"errors"
	"testing"
)

// ptr is a tiny helper — tests for this package care a lot about
// distinguishing nil / &0 / &N for uint32, so a one-line constructor
// keeps the test table readable.
func ptr(v uint32) *uint32 { return &v }

// TestResolve covers the NULL/0/N combinatorics for one field (disk)
// exhaustively, then spot-checks that the other five fields follow the
// same rule. If the one-field matrix is correct, the structural symmetry
// of the resolver (same pick() per field) makes the rest guaranteed.
func TestResolve_NullZeroOverrideCombinations(t *testing.T) {
	tests := []struct {
		name     string
		pkg      *PackageLimits
		override *OverrideLimits
		want     uint32 // DiskQuotaMB for brevity
	}{
		// --- No package, no override ---
		{"nil pkg + nil override → 0", nil, nil, 0},
		{"nil pkg + empty override → 0", nil, &OverrideLimits{}, 0},

		// --- Package only ---
		{"pkg=0, no override → 0", &PackageLimits{DiskQuotaMB: 0}, nil, 0},
		{"pkg=5GB, no override → 5GB", &PackageLimits{DiskQuotaMB: 5120}, nil, 5120},

		// --- Override wins (the critical NULL/0/N distinction) ---
		{"pkg=5GB, override nil → 5GB (inherit)", &PackageLimits{DiskQuotaMB: 5120}, &OverrideLimits{}, 5120},
		{"pkg=5GB, override=&0 → 0 (override to unlimited)", &PackageLimits{DiskQuotaMB: 5120}, &OverrideLimits{DiskQuotaMB: ptr(0)}, 0},
		{"pkg=5GB, override=&10GB → 10GB", &PackageLimits{DiskQuotaMB: 5120}, &OverrideLimits{DiskQuotaMB: ptr(10240)}, 10240},
		{"pkg=5GB, override=&1GB → 1GB (squeeze)", &PackageLimits{DiskQuotaMB: 5120}, &OverrideLimits{DiskQuotaMB: ptr(1024)}, 1024},

		// --- Override when pkg is zero ---
		{"pkg=0, override=&N → N (override from unlimited to capped)", &PackageLimits{DiskQuotaMB: 0}, &OverrideLimits{DiskQuotaMB: ptr(2048)}, 2048},
		{"pkg=0, override=&0 → 0 (no change)", &PackageLimits{DiskQuotaMB: 0}, &OverrideLimits{DiskQuotaMB: ptr(0)}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.pkg, tt.override).DiskQuotaMB
			if got != tt.want {
				t.Errorf("DiskQuotaMB = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestResolve_EveryFieldCascades ensures all six fields use the same
// override semantics. Distinct values per field catches any copy-paste
// bug in Resolve (e.g. a field reading from the wrong override pointer).
func TestResolve_EveryFieldCascades(t *testing.T) {
	pkg := &PackageLimits{
		DiskQuotaMB:     1000,
		CPUQuotaPercent: 100,
		MemoryLimitMB:   2000,
		IOReadMbps:      300,
		IOWriteMbps:     400,
		MaxTasks:        500,
	}
	ov := &OverrideLimits{
		DiskQuotaMB:     ptr(9000),
		CPUQuotaPercent: ptr(900),
		MemoryLimitMB:   ptr(9200),
		IOReadMbps:      ptr(930),
		IOWriteMbps:     ptr(940),
		MaxTasks:        ptr(950),
	}
	got := Resolve(pkg, ov)
	want := EffectiveLimits{
		DiskQuotaMB: 9000, CPUQuotaPercent: 900, MemoryLimitMB: 9200,
		IOReadMbps: 930, IOWriteMbps: 940, MaxTasks: 950,
	}
	if got != want {
		t.Errorf("Resolve = %+v, want %+v", got, want)
	}
}

// TestResolve_PartialOverride exercises the common admin workflow:
// "override memory only, inherit everything else from the package."
func TestResolve_PartialOverride(t *testing.T) {
	pkg := &PackageLimits{DiskQuotaMB: 1000, MemoryLimitMB: 2000, MaxTasks: 500}
	ov := &OverrideLimits{MemoryLimitMB: ptr(4000)} // only memory

	got := Resolve(pkg, ov)
	if got.DiskQuotaMB != 1000 {
		t.Errorf("DiskQuotaMB inherited: got %d, want 1000", got.DiskQuotaMB)
	}
	if got.MemoryLimitMB != 4000 {
		t.Errorf("MemoryLimitMB override: got %d, want 4000", got.MemoryLimitMB)
	}
	if got.MaxTasks != 500 {
		t.Errorf("MaxTasks inherited: got %d, want 500", got.MaxTasks)
	}
}

func TestMemoryHighMB(t *testing.T) {
	tests := []struct {
		in, want uint32
	}{
		{0, 0},      // unlimited → no soft limit either
		{100, 90},   // 90%
		{256, 230},  // floor(230.4) = 230
		{4096, 3686}, // 4 GB → ~3.6 GB soft
		{10, 9},
		{1, 0}, // floor(0.9) = 0 — degenerate but well-defined
	}
	for _, tt := range tests {
		if got := MemoryHighMB(tt.in); got != tt.want {
			t.Errorf("MemoryHighMB(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		limits EffectiveLimits
		wantErr bool
		wantField string
	}{
		{"all zero passes (unlimited is always legal)", EffectiveLimits{}, false, ""},
		{"sane values pass", EffectiveLimits{DiskQuotaMB: 5120, CPUQuotaPercent: 200, MemoryLimitMB: 4096, MaxTasks: 500}, false, ""},
		{"cpu at max passes", EffectiveLimits{CPUQuotaPercent: MaxCPUQuotaPercent}, false, ""},
		{"cpu over max fails", EffectiveLimits{CPUQuotaPercent: MaxCPUQuotaPercent + 1}, true, "cpu_quota_percent"},
		{"memory over max fails", EffectiveLimits{MemoryLimitMB: MaxMemoryLimitMB + 1}, true, "memory_limit_mb"},
		{"io_read over max fails", EffectiveLimits{IOReadMbps: MaxIOMbps + 1}, true, "io_read_mbps"},
		{"io_write over max fails", EffectiveLimits{IOWriteMbps: MaxIOMbps + 1}, true, "io_write_mbps"},
		{"tasks over max fails", EffectiveLimits{MaxTasks: MaxTasks + 1}, true, "max_tasks"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.limits.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				var be *BoundError
				if !errors.As(err, &be) {
					t.Fatalf("expected *BoundError, got %T", err)
				}
				if be.Field != tt.wantField {
					t.Errorf("field: got %q, want %q", be.Field, tt.wantField)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
