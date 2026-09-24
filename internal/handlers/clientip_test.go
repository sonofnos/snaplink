package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	cases := []struct {
		name, xff, remote string
		hops              int
		want              string
	}{
		{"render chain", "203.0.113.9, 172.70.46.9, 10.195.91.69", "10.1.1.1:5555", 2, "203.0.113.9"},
		{"spoofed prefix is ignored", "1.2.3.4,203.0.113.9, 172.70.46.9, 10.195.91.69", "10.1.1.1:5555", 2, "203.0.113.9"},
		{"header ignored when no proxies trusted", "1.2.3.4", "198.51.100.7:4444", 0, "198.51.100.7"},
		{"no header falls back to remote addr", "", "198.51.100.7:4444", 2, "198.51.100.7"},
		{"fewer entries than hops", "203.0.113.9", "10.1.1.1:5555", 2, "203.0.113.9"},
	}
	for _, c := range cases {
		h := &Handler{proxyHops: c.hops}
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := h.clientIP(r); got != c.want {
			t.Errorf("%s: clientIP = %q, want %q", c.name, got, c.want)
		}
	}
}
