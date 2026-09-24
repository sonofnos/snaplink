package handlers

import "testing"

func TestClassifyUA(t *testing.T) {
	cases := []struct{ ua, browser, system string }{
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36", "Chrome", "macOS"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "Safari", "macOS"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36 Edg/126.0", "Edge", "Windows"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Version/17.5 Mobile Safari/604.1", "Safari", "iOS"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0", "Firefox", "Linux"},
		{"k6/1.0", "Bot / CLI", "Other"},
		{"", "Unknown", "Unknown"},
	}
	for _, c := range cases {
		b, s := classifyUA(c.ua)
		if b != c.browser || s != c.system {
			t.Errorf("classifyUA(%q) = %s/%s, want %s/%s", c.ua, b, s, c.browser, c.system)
		}
	}
}

func TestReferrerHost(t *testing.T) {
	for in, want := range map[string]string{"": "Direct", "https://www.Google.com/search?q=x": "google.com", "not a url": "Direct"} {
		if got := referrerHost(in); got != want {
			t.Errorf("referrerHost(%q) = %q, want %q", in, got, want)
		}
	}
}
