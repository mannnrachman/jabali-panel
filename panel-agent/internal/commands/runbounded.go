package commands

import (
	"io"
	"os/exec"
	"sync"
)

// runBoundedOutput is a drop-in replacement for exec.Cmd.CombinedOutput()
// that keeps only the last `tailBytes` of stdout+stderr, discarding the
// rest as it streams. Protects the agent from heavy-output children
// (composer, drush, wp-cli, matomo, concrete) whose progress/debug logs
// can exceed gigabytes and OOM-kill the agent at 9+ GB RSS when
// CombinedOutput() buffers unbounded.
//
// The returned bytes are the TAIL of the combined stream — first-seen
// output is dropped once we overflow. That matches the agent's actual
// use of the output (error messages only, truncated to 1024 bytes
// before being stamped onto last_error), and keeps memory bounded to
// `tailBytes` regardless of child runtime.
//
// 16 KiB is the default tail size because it's generous for PHP
// stack traces (the usual failure shape is ~1-4 KiB of error + file +
// line) while costing ~15 MB across 1000 concurrent installs.
const defaultTailBytes = 16 * 1024

func runBoundedOutput(cmd *exec.Cmd, tailBytes int) ([]byte, error) {
	if tailBytes <= 0 {
		tailBytes = defaultTailBytes
	}
	rb := newRingBuffer(tailBytes)
	// Combine stdout + stderr into the same ring — same semantics as
	// CombinedOutput, just bounded. The writer is goroutine-safe so
	// Cmd.Wait()'s concurrent stdout/stderr copiers don't interleave
	// into a half-written byte.
	cmd.Stdout = rb
	cmd.Stderr = rb
	err := cmd.Run()
	return rb.Bytes(), err
}

// ringBuffer is a bounded io.Writer that keeps the last `cap` bytes of
// everything written to it. Over-capacity writes advance a logical
// start offset; reads materialise the contents in chronological order.
//
// Locking is coarse: one mutex guards the whole struct. The only
// writer contention is between Cmd's stdout+stderr goroutines, which
// alternate short writes — contention is negligible in practice.
type ringBuffer struct {
	mu    sync.Mutex
	buf   []byte
	size  int  // current logical byte count (<= cap)
	start int  // offset into buf of the oldest byte when full
	total int  // total bytes ever written (for overflow detection)
	cap   int
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{
		buf: make([]byte, capacity),
		cap: capacity,
	}
}

// Write implements io.Writer. Copies the tail of p that fits into the
// ring; discards the prefix if p itself exceeds cap.
func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(p)
	r.total += n

	// If p is bigger than cap, we only keep its last cap bytes.
	if n > r.cap {
		p = p[n-r.cap:]
	}

	for _, b := range p {
		if r.size < r.cap {
			r.buf[(r.start+r.size)%r.cap] = b
			r.size++
		} else {
			r.buf[r.start] = b
			r.start = (r.start + 1) % r.cap
		}
	}
	return n, nil
}

// Bytes returns the current contents in chronological order. Caller
// must not mutate the returned slice — it's a fresh copy.
func (r *ringBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, r.size)
	if r.size == 0 {
		return out
	}
	// Linear copy when the logical contents don't wrap; two-slice
	// copy when they do.
	if r.start+r.size <= r.cap {
		copy(out, r.buf[r.start:r.start+r.size])
	} else {
		first := r.cap - r.start
		copy(out, r.buf[r.start:])
		copy(out[first:], r.buf[:r.size-first])
	}
	return out
}

// Compile-time check: ringBuffer satisfies io.Writer.
var _ io.Writer = (*ringBuffer)(nil)
