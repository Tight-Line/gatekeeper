package ipfilter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type errorReadCloser struct {
	err error
}

func (e errorReadCloser) Read(p []byte) (int, error) {
	return 0, e.err
}

func (e errorReadCloser) Close() error {
	return nil
}

func TestExtractCIDRsWithJQ_AWSFormat(t *testing.T) {
	// Sample AWS IP ranges JSON
	data := []byte(`{
		"syncToken": "1234567890",
		"createDate": "2024-01-01-00-00-00",
		"prefixes": [
			{"ip_prefix": "3.5.140.0/22", "region": "us-east-1", "service": "EC2"},
			{"ip_prefix": "15.230.56.104/31", "region": "us-west-2", "service": "S3"},
			{"ip_prefix": "52.93.178.234/32", "region": "eu-west-1", "service": "EC2"}
		]
	}`)

	// Extract all prefixes
	cidrs, err := extractCIDRsWithJQ(data, ".prefixes[].ip_prefix")
	if err != nil {
		t.Fatalf("failed to extract CIDRs: %v", err)
	}

	if len(cidrs) != 3 {
		t.Errorf("expected 3 CIDRs, got %d", len(cidrs))
	}

	expected := []string{"3.5.140.0/22", "15.230.56.104/31", "52.93.178.234/32"}
	for i, cidr := range cidrs {
		if cidr != expected[i] {
			t.Errorf("expected %s at index %d, got %s", expected[i], i, cidr)
		}
	}
}

func TestExtractCIDRsWithJQ_AWSFilteredByService(t *testing.T) {
	data := []byte(`{
		"prefixes": [
			{"ip_prefix": "3.5.140.0/22", "service": "EC2"},
			{"ip_prefix": "15.230.56.104/31", "service": "S3"},
			{"ip_prefix": "52.93.178.234/32", "service": "EC2"}
		]
	}`)

	// Extract only EC2 prefixes
	cidrs, err := extractCIDRsWithJQ(data, `.prefixes[] | select(.service=="EC2") | .ip_prefix`)
	if err != nil {
		t.Fatalf("failed to extract CIDRs: %v", err)
	}

	if len(cidrs) != 2 {
		t.Errorf("expected 2 CIDRs, got %d", len(cidrs))
	}

	expected := []string{"3.5.140.0/22", "52.93.178.234/32"}
	for i, cidr := range cidrs {
		if cidr != expected[i] {
			t.Errorf("expected %s at index %d, got %s", expected[i], i, cidr)
		}
	}
}

func TestExtractCIDRsWithJQ_GoogleCloudFormat(t *testing.T) {
	data := []byte(`{
		"syncToken": "1234567890",
		"creationTime": "2024-01-01T00:00:00.000000",
		"prefixes": [
			{"ipv4Prefix": "34.80.0.0/15"},
			{"ipv6Prefix": "2600:1900::/35"},
			{"ipv4Prefix": "35.185.0.0/17"}
		]
	}`)

	cidrs, err := extractCIDRsWithJQ(data, ".prefixes[].ipv4Prefix")
	if err != nil {
		t.Fatalf("failed to extract CIDRs: %v", err)
	}

	if len(cidrs) != 2 {
		t.Errorf("expected 2 CIDRs, got %d", len(cidrs))
	}

	expected := []string{"34.80.0.0/15", "35.185.0.0/17"}
	for i, cidr := range cidrs {
		if cidr != expected[i] {
			t.Errorf("expected %s at index %d, got %s", expected[i], i, cidr)
		}
	}
}

func TestExtractCIDRsWithJQ_InvalidJSON(t *testing.T) {
	data := []byte(`not valid json`)

	_, err := extractCIDRsWithJQ(data, ".prefixes[].ip_prefix")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestExtractCIDRsWithJQ_InvalidJQQuery(t *testing.T) {
	data := []byte(`{"prefixes": []}`)

	_, err := extractCIDRsWithJQ(data, ".invalid[[[")
	if err == nil {
		t.Error("expected error for invalid jq query")
	}
}

func TestExtractCIDRsWithJQ_NoResults(t *testing.T) {
	data := []byte(`{"prefixes": []}`)

	_, err := extractCIDRsWithJQ(data, ".prefixes[].ip_prefix")
	if err == nil {
		t.Error("expected error for no results")
	}
}

func TestFetcher_FetchAndUpdate(t *testing.T) {
	// Create test server that returns IP ranges
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"prefixes": [
				{"ip_prefix": "10.0.0.0/8"},
				{"ip_prefix": "192.168.0.0/16"}
			]
		}`))
	}))
	defer server.Close()

	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	fetcher.AddSource(&FetchSource{
		Name:            "test",
		URL:             server.URL,
		JQQuery:         ".prefixes[].ip_prefix",
		RefreshInterval: 1 * time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher.Start(ctx)

	// Wait for initial fetch
	time.Sleep(100 * time.Millisecond)

	// Check that filter was created
	if !filters.Allow("test", "10.0.0.1") {
		t.Error("expected 10.0.0.1 to be allowed")
	}
	if !filters.Allow("test", "192.168.1.1") {
		t.Error("expected 192.168.1.1 to be allowed")
	}
	if filters.Allow("test", "8.8.8.8") {
		t.Error("expected 8.8.8.8 to be blocked")
	}
}

func TestFetcher_HandlesFetchError(t *testing.T) {
	// Create test server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	fetcher.AddSource(&FetchSource{
		Name:            "test",
		URL:             server.URL,
		JQQuery:         ".prefixes[].ip_prefix",
		RefreshInterval: 1 * time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher.Start(ctx)

	// Wait for initial fetch attempt
	time.Sleep(100 * time.Millisecond)

	// Filter should not exist since fetch failed
	_, exists := filters.Get("test")
	if exists {
		t.Error("expected filter to not exist after failed fetch")
	}
}

func TestFetcher_Stop(t *testing.T) {
	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	ctx := context.Background()
	fetcher.Start(ctx)

	// Call Stop to cancel the context
	fetcher.Stop()

	// No panic or error means success
}

func TestFetcher_StopWithoutStart(t *testing.T) {
	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	// Stop without Start - should not panic (cancel is nil)
	fetcher.Stop()
}

func TestFetcher_InvalidCIDRsFromSource(t *testing.T) {
	// Server returns invalid CIDRs
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"prefixes": [
				{"ip_prefix": "not-a-valid-cidr"}
			]
		}`))
	}))
	defer server.Close()

	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	fetcher.AddSource(&FetchSource{
		Name:            "test",
		URL:             server.URL,
		JQQuery:         ".prefixes[].ip_prefix",
		RefreshInterval: 1 * time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Filter should not exist since CIDRs were invalid
	_, exists := filters.Get("test")
	if exists {
		t.Error("expected filter to not exist after invalid CIDRs")
	}
}

func TestFetcher_UpdateExistingFilterWithInvalidCIDRs(t *testing.T) {
	// First request returns valid CIDRs, second returns invalid
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		if callCount == 1 {
			_, _ = w.Write([]byte(`{"prefixes": [{"ip_prefix": "10.0.0.0/8"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"prefixes": [{"ip_prefix": "invalid"}]}`))
		}
	}))
	defer server.Close()

	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	fetcher.AddSource(&FetchSource{
		Name:            "test",
		URL:             server.URL,
		JQQuery:         ".prefixes[].ip_prefix",
		RefreshInterval: 50 * time.Millisecond, // Short interval for test
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// First fetch should succeed
	filter, exists := filters.Get("test")
	if !exists {
		t.Fatal("expected filter to exist after first fetch")
	}
	if !filter.Allow("10.0.0.1") {
		t.Error("expected 10.0.0.1 to be allowed")
	}

	// Wait for second refresh attempt
	time.Sleep(100 * time.Millisecond)

	// Filter should still exist with old CIDRs (update failed silently)
	if !filter.Allow("10.0.0.1") {
		t.Error("expected 10.0.0.1 to still be allowed after failed update")
	}
}

func TestFetcher_DefaultRefreshInterval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"prefixes": [{"ip_prefix": "10.0.0.0/8"}]}`))
	}))
	defer server.Close()

	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	// RefreshInterval is 0 - should default to 24h
	fetcher.AddSource(&FetchSource{
		Name:            "test",
		URL:             server.URL,
		JQQuery:         ".prefixes[].ip_prefix",
		RefreshInterval: 0, // Will default to 24h
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Filter should exist from initial fetch
	_, exists := filters.Get("test")
	if !exists {
		t.Error("expected filter to exist")
	}
}

func TestExtractCIDRsWithJQ_RuntimeError(t *testing.T) {
	// Trigger a jq runtime error by having query operate on wrong type
	data := []byte(`{"value": 42}`)

	// This query tries to iterate over a number which should cause a runtime error
	_, err := extractCIDRsWithJQ(data, ".value[]")
	if err == nil {
		t.Error("expected error for jq runtime error")
	}
}

func TestFetcher_InvalidURL(t *testing.T) {
	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	// Use an invalid URL that will fail in NewRequestWithContext
	fetcher.AddSource(&FetchSource{
		Name:            "test",
		URL:             "://invalid-url", // Invalid URL scheme
		JQQuery:         ".prefixes[].ip_prefix",
		RefreshInterval: 1 * time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Filter should not exist since URL was invalid
	_, exists := filters.Get("test")
	if exists {
		t.Error("expected filter to not exist after invalid URL")
	}
}

func TestFetcher_ReadBodyError(t *testing.T) {
	// Server returns a response where reading the body will fail
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000") // Claim big body
		w.WriteHeader(http.StatusOK)
		// Don't write full body - this should cause read error
		_, _ = w.Write([]byte("partial"))
		// Force flush and close
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)

	fetcher.AddSource(&FetchSource{
		Name:            "test",
		URL:             server.URL,
		JQQuery:         ".prefixes[].ip_prefix",
		RefreshInterval: 1 * time.Hour,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fetcher.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// The body read might fail or succeed with partial data
	// Either way, filter shouldn't work correctly
}

func TestFetcher_Fetch_RequestError(t *testing.T) {
	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)
	fetcher.client = &http.Client{
		Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("transport error")
		}),
	}

	_, err := fetcher.fetch(context.Background(), &FetchSource{
		Name:    "test",
		URL:     "http://example.com",
		JQQuery: ".prefixes[].ip_prefix",
	})
	if err == nil {
		t.Fatal("expected error for request failure")
	}
}

func TestFetcher_Fetch_ReadError(t *testing.T) {
	filters := NewFilterSet()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fetcher := NewFetcher(filters, logger)
	fetcher.client = &http.Client{
		Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       errorReadCloser{err: errors.New("read failed")},
				Header:     make(http.Header),
			}, nil
		}),
	}

	_, err := fetcher.fetch(context.Background(), &FetchSource{
		Name:    "test",
		URL:     "http://example.com",
		JQQuery: ".prefixes[].ip_prefix",
	})
	if err == nil {
		t.Fatal("expected error for body read failure")
	}
}
