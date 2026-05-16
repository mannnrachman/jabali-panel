package commands

import (
	"bytes"
	"net"
	"testing"
)

// TestTPFrameRoundTrip — the opcode framing must survive write→read for
// every opcode, including empty and binary payloads, since the panel-api
// bridge (api.udsWriteFrame/udsReadFrame) speaks the identical wire and
// any drift silently corrupts a root shell.
func TestTPFrameRoundTrip(t *testing.T) {
	cases := []struct {
		op   byte
		data []byte
	}{
		{tpOpInit, []byte(`{"session_id":"01ABC","cols":80,"rows":24}`)},
		{tpOpStdin, []byte("ls -la\r")},
		{tpOpStdout, []byte{0x1b, '[', '0', 'm', 0x00, 0xff}}, // ANSI + NUL + 0xff
		{tpOpResize, []byte(`{"cols":120,"rows":40}`)},
		{tpOpExit, beU32(0)},
		{tpOpStdin, nil}, // zero-length frame
	}
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()

	go func() {
		w := &tpConn{c: cli}
		for _, c := range cases {
			if err := w.write(c.op, c.data); err != nil {
				t.Errorf("write op=%#x: %v", c.op, err)
				return
			}
		}
	}()

	for i, c := range cases {
		op, payload, err := tpReadFrame(srv)
		if err != nil {
			t.Fatalf("case %d read: %v", i, err)
		}
		if op != c.op {
			t.Fatalf("case %d op: got %#x want %#x", i, op, c.op)
		}
		if !bytes.Equal(payload, c.data) && !(len(payload) == 0 && len(c.data) == 0) {
			t.Fatalf("case %d payload: got %v want %v", i, payload, c.data)
		}
	}
}

// TestTPFrameOversizeRejected — a length header beyond the 1 MiB guard
// must error rather than allocate, so a hostile/buggy peer can't OOM
// the agent.
func TestTPFrameOversizeRejected(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()
	go func() {
		// opcode + a 4-byte length of 0xFFFFFFFF, no body.
		_, _ = cli.Write([]byte{tpOpStdin, 0xff, 0xff, 0xff, 0xff})
	}()
	if _, _, err := tpReadFrame(srv); err == nil {
		t.Fatal("expected oversize frame to be rejected")
	}
}

// TestSanitizeSessionID — the .cast filename is derived from the
// client-supplied session id; it MUST be confined to the ULID charset
// so a crafted id cannot traverse out of /var/log/jabali/terminal.
func TestSanitizeSessionID(t *testing.T) {
	cases := map[string]string{
		"01HXABCDEF0123456789GHJKMN": "01HXABCDEF0123456789GHJKMN",
		"../../etc/passwd":           "etcpasswd",
		"/abs/path":                  "abspath",
		"a b\tc":                     "abc",
		"":                          "invalid",
		"...":                       "invalid",
	}
	for in, want := range cases {
		if got := sanitizeSessionID(in); got != want {
			t.Errorf("sanitizeSessionID(%q) = %q, want %q", in, got, want)
		}
	}
	// Length is capped (no unbounded filename).
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'A'
	}
	if got := sanitizeSessionID(string(long)); len(got) > 32 {
		t.Errorf("sanitized id not length-capped: %d", len(got))
	}
}
