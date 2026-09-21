package relayclient

import (
	"net/http"
	"time"
)

const (
	// maxIdleConnsPerHost is the per-host idle connection limit for the shared
	// transport. Go's DefaultTransport leaves this unset, which means
	// http.DefaultMaxIdleConnsPerHost, or 2. That is far too low here: a relay
	// runs one long-poll per channel against the same gatekeeperd, so on
	// HTTP/1.1 it holds one connection per channel and keeps only 2 of them when
	// they return. Every other channel re-dials and re-handshakes on each poll
	// cycle, and sendResponse competes for those same 2 slots, which puts a TCP
	// and TLS handshake in the path of most webhook responses. This limit has to
	// clear the channel count.
	maxIdleConnsPerHost = 256

	// maxIdleConnsTotal caps idle connections across every host. It has to stay
	// above maxIdleConnsPerHost or it becomes the binding limit instead, and it
	// leaves room for the destination hosts the forwarders talk to.
	maxIdleConnsTotal = 512
)

// sharedTransport backs every HTTP client the relay creates. Sharing one
// instance is deliberate: a transport per client would pool connections per
// client and reuse none of them between channels.
var sharedTransport = newTransport()

// newTransport returns a transport sized for a relay with many channels.
func newTransport() *http.Transport {
	// Clone DefaultTransport rather than build one from scratch, so proxy
	// support, dial and handshake timeouts, and HTTP/2 negotiation all keep
	// tracking the standard library's defaults.
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = maxIdleConnsTotal
	t.MaxIdleConnsPerHost = maxIdleConnsPerHost
	return t
}

// newHTTPClient returns a client backed by the shared transport.
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: sharedTransport,
	}
}
