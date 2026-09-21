package relayclient

import (
	"net/http"
	"testing"
	"time"
)

func TestSharedTransportRaisesIdleLimits(t *testing.T) {
	// Go leaves MaxIdleConnsPerHost unset on DefaultTransport, which means
	// http.DefaultMaxIdleConnsPerHost (2). A relay with more channels than that
	// re-dials and re-handshakes on every poll cycle.
	if sharedTransport.MaxIdleConnsPerHost <= http.DefaultMaxIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want more than the stdlib default %d",
			sharedTransport.MaxIdleConnsPerHost, http.DefaultMaxIdleConnsPerHost)
	}
	if sharedTransport.MaxIdleConnsPerHost != maxIdleConnsPerHost {
		t.Errorf("MaxIdleConnsPerHost = %d, want %d",
			sharedTransport.MaxIdleConnsPerHost, maxIdleConnsPerHost)
	}

	// The total has to stay above the per-host limit, or it binds first.
	if sharedTransport.MaxIdleConns != maxIdleConnsTotal {
		t.Errorf("MaxIdleConns = %d, want %d", sharedTransport.MaxIdleConns, maxIdleConnsTotal)
	}
	if sharedTransport.MaxIdleConns < sharedTransport.MaxIdleConnsPerHost {
		t.Errorf("MaxIdleConns %d is below MaxIdleConnsPerHost %d, so it binds first",
			sharedTransport.MaxIdleConns, sharedTransport.MaxIdleConnsPerHost)
	}

	// Cloning DefaultTransport rather than building one keeps HTTP/2 and proxy
	// support, which a hand-built transport would silently drop.
	if !sharedTransport.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 = false, want the DefaultTransport behavior")
	}
	if sharedTransport.Proxy == nil {
		t.Error("Proxy = nil, want the DefaultTransport behavior")
	}
}

func TestNewTransportLeavesDefaultTransportAlone(t *testing.T) {
	dt, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not an *http.Transport")
	}
	if dt.MaxIdleConnsPerHost != 0 {
		t.Errorf("http.DefaultTransport.MaxIdleConnsPerHost = %d, want 0: newTransport must clone, not mutate",
			dt.MaxIdleConnsPerHost)
	}
}

func TestNewHTTPClientSharesOneTransport(t *testing.T) {
	a := newHTTPClient(60 * time.Second)
	b := newHTTPClient(30 * time.Second)

	if a.Timeout != 60*time.Second {
		t.Errorf("Timeout = %v, want 60s", a.Timeout)
	}
	if b.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", b.Timeout)
	}
	// A transport per client would pool connections per client and reuse none
	// of them between channels.
	if a.Transport != b.Transport {
		t.Error("clients got different transports, want the shared one")
	}
	if a.Transport != http.RoundTripper(sharedTransport) {
		t.Error("client is not backed by sharedTransport")
	}
}
