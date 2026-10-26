package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dgoumtsop/drift/internal/ratelimit"
)

func TestExtractIPIgnoresHeadersByDefault(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:51234"
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	r.Header.Set("X-Real-IP", "7.7.7.7")

	if got := extractIP(r, false); got != "10.0.0.5" {
		t.Fatalf("got %q, want RemoteAddr host 10.0.0.5", got)
	}
}

func TestExtractIPTrustedHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:51234"
	r.Header.Set("X-Forwarded-For", " 203.0.113.9 , 10.0.0.1")
	if got := extractIP(r, true); got != "203.0.113.9" {
		t.Fatalf("got %q, want leftmost XFF entry", got)
	}

	r.Header.Del("X-Forwarded-For")
	r.Header.Set("X-Real-IP", "198.51.100.2")
	if got := extractIP(r, true); got != "198.51.100.2" {
		t.Fatalf("got %q, want X-Real-IP", got)
	}
}

func TestExtractIPv6(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[::1]:8080"
	if got := extractIP(r, false); got != "::1" {
		t.Fatalf("got %q, want ::1", got)
	}
}

func newTestProxy(t *testing.T, capacity float64) (*ReverseProxy, *string) {
	t.Helper()
	gotHost := new(string)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	rp, err := New(backend.URL, ratelimit.NewInMemory(capacity, 0.0001), false)
	if err != nil {
		t.Fatal(err)
	}
	return rp, gotHost
}

func TestProxyForwardsThenRateLimits(t *testing.T) {
	rp, gotHost := newTestProxy(t, 2)

	codes := []int{}
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "http://gateway.local/get", nil)
		r.RemoteAddr = "10.0.0.5:1111"
		rp.ServeHTTP(w, r)
		codes = append(codes, w.Code)
		if w.Code == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
			t.Fatal("429 response should set Retry-After")
		}
	}

	want := []int{200, 200, 429}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("status codes = %v, want %v", codes, want)
		}
	}
	if *gotHost == "gateway.local" {
		t.Fatal("backend received the gateway's Host header instead of its own")
	}
}

func TestSpoofedXFFDoesNotBypassLimiter(t *testing.T) {
	rp, _ := newTestProxy(t, 1)

	limited := false
	for _, fake := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.5:1111"
		r.Header.Set("X-Forwarded-For", fake)
		rp.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Fatal("rotating X-Forwarded-For let one client bypass the limiter")
	}
}

func TestUpstreamDownReturns502(t *testing.T) {
	rp, err := New("http://127.0.0.1:1", ratelimit.NewInMemory(10, 1), false)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	rp.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", w.Code)
	}
}
