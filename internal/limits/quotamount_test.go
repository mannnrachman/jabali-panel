package limits

import (
	"os"
	"path/filepath"
	"testing"
)

// Helper — writes a fake /proc/mounts file to a tempfile and returns
// the path. Each line has the 6 whitespace-separated fields that
// /proc/mounts really has, just the fields we parse are meaningful.
func writeMounts(t *testing.T, lines []string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "mounts")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestQuotaMountFor_HomeOnOwnMount(t *testing.T) {
	mp := writeMounts(t, []string{
		"/dev/sda1 / ext4 rw,relatime 0 0",
		"/dev/sda2 /home ext4 rw,relatime,usrquota 0 0",
		"/dev/sda3 /var ext4 rw,relatime 0 0",
	})
	got, err := quotaMountForWithMounts("/home/shuki/public_html", mp)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/home" {
		t.Errorf("got %q, want /home — longest-prefix must beat /", got)
	}
}

func TestQuotaMountFor_HomeOnRoot(t *testing.T) {
	mp := writeMounts(t, []string{
		"/dev/sda1 / ext4 rw,relatime 0 0",
	})
	got, err := quotaMountForWithMounts("/home/shuki", mp)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/" {
		t.Errorf("got %q, want /", got)
	}
}

func TestQuotaMountFor_NoRootEntry(t *testing.T) {
	// A corrupted mounts file without "/" should fail loud, not return
	// some arbitrary match.
	mp := writeMounts(t, []string{
		"/dev/sda1 /boot ext4 rw 0 0",
	})
	_, err := quotaMountForWithMounts("/home/shuki", mp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQuotaMountFor_MountsFileMissing(t *testing.T) {
	_, err := quotaMountForWithMounts("/home", filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("expected open error, got nil")
	}
}

func TestQuotaMountFor_PathEqualsMount(t *testing.T) {
	// Asking for the mount itself (/home) must return /home, not
	// a longer prefix that happens to be below /home.
	mp := writeMounts(t, []string{
		"/dev/sda1 / ext4 rw 0 0",
		"/dev/sda2 /home ext4 rw,usrquota 0 0",
		"/dev/sda3 /home/shuki/deep-mount xfs rw 0 0",
	})
	got, err := quotaMountForWithMounts("/home", mp)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/home" {
		t.Errorf("got %q, want /home", got)
	}
}

func TestQuotaMountFor_NestedMountWins(t *testing.T) {
	// If /home has its own mount AND /home/shuki has a nested mount,
	// a path inside /home/shuki must land on the deeper mount because
	// that's where setquota's quota table lives.
	mp := writeMounts(t, []string{
		"/dev/sda1 / ext4 rw 0 0",
		"/dev/sda2 /home ext4 rw,usrquota 0 0",
		"/dev/sda3 /home/shuki xfs rw,usrquota 0 0",
	})
	got, err := quotaMountForWithMounts("/home/shuki/public_html", mp)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/home/shuki" {
		t.Errorf("got %q, want /home/shuki — nested mount must win", got)
	}
}
