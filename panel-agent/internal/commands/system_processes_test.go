package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProcStat_NormalCase(t *testing.T) {
	const sample = "1234 (sh) S 1 1234 1234 0 -1 4194304 100 0 0 0"
	comm, state, err := parseProcStat(sample)
	require.NoError(t, err)
	assert.Equal(t, "sh", comm)
	assert.Equal(t, "S", state)
}

func TestParseProcStat_CommWithParens(t *testing.T) {
	// kernel threads + apps with weird names produce these. The parser
	// must use FIRST '(' and LAST ')' or it misreads "(rcu_par_(test))"
	// as comm="rcu_par_(test", state=corrupted.
	const sample = "999 (rcu_par_(test)) R 2 0 0 0 -1 69238880 0 0"
	comm, state, err := parseProcStat(sample)
	require.NoError(t, err)
	assert.Equal(t, "rcu_par_(test)", comm)
	assert.Equal(t, "R", state)
}

func TestParseProcStat_Malformed(t *testing.T) {
	_, _, err := parseProcStat("not-a-stat-line")
	assert.Error(t, err)
}

func TestParseProcStatm(t *testing.T) {
	// Pages: size=100, resident=50.
	rss, err := parseProcStatm("100 50 30 1 0 0 0\n")
	require.NoError(t, err)
	// 50 pages × 4096 / 1024 = 200 (assuming 4 KB pages).
	expectedKB := uint64(50) * uint64(os.Getpagesize()) / 1024
	assert.Equal(t, expectedKB, rss)
}

func TestParseProcStatm_Malformed(t *testing.T) {
	_, err := parseProcStatm("100")
	assert.Error(t, err)
}

func TestSystemProcessesHandler_FixtureProc(t *testing.T) {
	dir := t.TempDir()

	// Build a fake /proc with three "processes":
	//   100 (init)   S  RSS 1000 pages
	//   200 (sleeper) S  RSS 50 pages
	//   300 (zombie) Z  RSS 0 pages
	mkProc := func(pid int, comm, state string, rssPages uint64, uid uint32) {
		pidDir := filepath.Join(dir, fmt.Sprintf("%d", pid))
		require.NoError(t, os.MkdirAll(pidDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(pidDir, "stat"),
			[]byte(fmt.Sprintf("%d (%s) %s 1 1 1 0 -1 4194304 0 0", pid, comm, state)), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(pidDir, "statm"),
			[]byte(fmt.Sprintf("%d %d 0 0 0 0 0", rssPages, rssPages)), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(pidDir, "status"),
			[]byte(fmt.Sprintf("Name:\t%s\nUid:\t%d\t%d\t%d\t%d\n", comm, uid, uid, uid, uid)), 0o644))
	}
	mkProc(100, "init", "S", 1000, 0)
	mkProc(200, "sleeper", "S", 50, 1000)
	mkProc(300, "zombie", "Z", 0, 1000)
	// Decoy non-pid directories — must not crash the scan.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "self"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sys"), 0o755))

	t.Cleanup(func(orig string) func() { return func() { procDir = orig } }(procDir))
	procDir = dir

	resp, err := systemProcessesHandler(context.Background(), nil)
	require.NoError(t, err)
	out := resp.(SystemProcessesResponse)
	assert.Equal(t, 3, out.Total)
	assert.Equal(t, 2, out.Sleeping)
	assert.Equal(t, 1, out.Zombie)
	require.GreaterOrEqual(t, len(out.TopByRSS), 1)
	// Top by RSS must be the 1000-pages init.
	assert.Equal(t, 100, out.TopByRSS[0].PID)
	assert.Equal(t, "init", out.TopByRSS[0].Comm)
}
