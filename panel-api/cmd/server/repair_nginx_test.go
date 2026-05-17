package main

import "testing"

func TestNginxVersionLT1251(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"nginx version: nginx/1.24.0 (Ubuntu)", true},
		{"nginx/1.24.0", true},
		{"1.24.0", true},
		{"1.18.0", true},
		{"1.25.0", true},  // 1.25.0 < 1.25.1
		{"1.25.1", false}, // first version with `http2 on;`
		{"1.25.3", false},
		{"1.26.0", false},
		{"1.27.4", false},
		{"", false},          // unknown -> conservative, do not flag
		{"garbage", false},   // unparseable -> conservative
		{"nginx/2.0.0", false},
	}
	for _, c := range cases {
		if got := nginxVersionLT1251(c.in); got != c.want {
			t.Errorf("nginxVersionLT1251(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFoldHTTP2(t *testing.T) {
	// broken default.conf (the 27ed1030-era output mx has)
	broken := `server {
    listen 443 ssl default_server;
    http2 on;
    listen [::]:443 ssl default_server;
    server_name _;
}
`
	wantDefault := `server {
    listen 443 ssl default_server http2;
    listen [::]:443 ssl default_server http2;
    server_name _;
}
`
	out, changed := foldHTTP2(broken)
	if !changed {
		t.Fatal("foldHTTP2(broken default) changed=false, want true")
	}
	if out != wantDefault {
		t.Errorf("foldHTTP2(broken default):\n--- got ---\n%s\n--- want ---\n%s", out, wantDefault)
	}

	// idempotent: second pass is a no-op
	out2, changed2 := foldHTTP2(out)
	if changed2 {
		t.Error("foldHTTP2 not idempotent: second pass reported changed=true")
	}
	if out2 != out {
		t.Errorf("foldHTTP2 not idempotent: out2 != out\n%s", out2)
	}

	// already-correct -> unchanged
	good := "    listen 443 ssl default_server http2;\n"
	if g, ch := foldHTTP2(good); ch || g != good {
		t.Errorf("foldHTTP2(already-correct) changed=%v out=%q, want false / unchanged", ch, g)
	}

	// panel vhost form: listen 8443 ssl; + http2 on;
	panelIn := "server {\n  listen 8443 ssl;\n  http2 on;\n}\n"
	panelWant := "server {\n  listen 8443 ssl http2;\n}\n"
	if g, ch := foldHTTP2(panelIn); !ch || g != panelWant {
		t.Errorf("foldHTTP2(panel) =\n%q\nwant\n%q (changed=%v)", g, panelWant, ch)
	}

	// must NOT add http2 to plain :80 listeners, only ssl listeners
	port80 := "server {\n    listen 80 default_server;\n    listen [::]:80 default_server;\n    http2 on;\n}\n"
	want80 := "server {\n    listen 80 default_server;\n    listen [::]:80 default_server;\n}\n"
	if g, ch := foldHTTP2(port80); !ch || g != want80 {
		t.Errorf("foldHTTP2(:80) =\n%q\nwant\n%q (changed=%v) — http2 must not touch non-ssl listen", g, want80, ch)
	}

	// specific-IP ssl listener still gets http2 folded
	ipIn := "    listen 203.0.113.5:443 ssl default_server;\n    http2 on;\n"
	ipWant := "    listen 203.0.113.5:443 ssl default_server http2;\n"
	if g, ch := foldHTTP2(ipIn); !ch || g != ipWant {
		t.Errorf("foldHTTP2(specific-ip) =\n%q\nwant\n%q (changed=%v)", g, ipWant, ch)
	}

	// no http2 anywhere -> unchanged
	plain := "server {\n    listen 80;\n    root /var/www;\n}\n"
	if g, ch := foldHTTP2(plain); ch || g != plain {
		t.Errorf("foldHTTP2(plain) changed=%v, want false/unchanged", ch)
	}
}
