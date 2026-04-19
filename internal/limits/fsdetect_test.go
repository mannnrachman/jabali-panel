package limits

import (
	"errors"
	"testing"
)

func TestDetectFilesystem_MapsKernelNames(t *testing.T) {
	// Override detectFS so the test doesn't shell out.
	origDetect := detectFS
	t.Cleanup(func() { detectFS = origDetect })

	tests := []struct {
		raw  string
		want FilesystemType
	}{
		{"ext4", FSExt4},
		{"ext2/ext3", FSExt3},
		{"ext3", FSExt3},
		{"ext2", FSExt2},
		{"xfs", FSXFS},
		{"btrfs", FSBtrfs},
		{"zfs", FSZFS},
		{"tmpfs", FSTmpfs},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			detectFS = func(string) (string, error) { return tt.raw, nil }
			got, err := DetectFilesystem("/home")
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestDetectFilesystem_UnknownTypeReturnsLowercasedRaw(t *testing.T) {
	origDetect := detectFS
	t.Cleanup(func() { detectFS = origDetect })
	detectFS = func(string) (string, error) { return "F2FS", nil }
	got, err := DetectFilesystem("/home")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "f2fs" {
		t.Errorf("got %q want %q (lowercased raw)", got, "f2fs")
	}
	// Unknown types always lack POSIX quota support.
	if got.SupportsPOSIXQuota() {
		t.Errorf("unknown FS should not claim POSIX quota support")
	}
}

func TestDetectFilesystem_StatError(t *testing.T) {
	origDetect := detectFS
	t.Cleanup(func() { detectFS = origDetect })
	wantErr := errors.New("stat exploded")
	detectFS = func(string) (string, error) { return "", wantErr }
	got, err := DetectFilesystem("/home")
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v want %v", err, wantErr)
	}
	if got != FSUnknown {
		t.Errorf("on error, expected FSUnknown, got %q", got)
	}
}

func TestSupportsPOSIXQuota(t *testing.T) {
	supported := []FilesystemType{FSExt2, FSExt3, FSExt4, FSXFS}
	unsupported := []FilesystemType{FSBtrfs, FSZFS, FSTmpfs, FSOther, FSUnknown, "f2fs"}
	for _, fs := range supported {
		if !fs.SupportsPOSIXQuota() {
			t.Errorf("%s should support POSIX quota", fs)
		}
	}
	for _, fs := range unsupported {
		if fs.SupportsPOSIXQuota() {
			t.Errorf("%s should NOT support POSIX quota", fs)
		}
	}
}
