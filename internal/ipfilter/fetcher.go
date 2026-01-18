package ipfilter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/itchyny/gojq"

	"github.com/tight-line/gatekeeper/internal/metrics"
)

// Fetcher periodically fetches IP ranges from remote sources and updates filters
type Fetcher struct {
	client  *http.Client
	logger  *slog.Logger
	filters *FilterSet

	mu      sync.Mutex
	sources map[string]*FetchSource
	cancel  context.CancelFunc
}

// FetchSource defines a remote IP range source
type FetchSource struct {
	Name            string
	URL             string
	JQQuery         string
	RefreshInterval time.Duration
}

// NewFetcher creates a new IP range fetcher
func NewFetcher(filters *FilterSet, logger *slog.Logger) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger:  logger,
		filters: filters,
		sources: make(map[string]*FetchSource),
	}
}

// AddSource adds a source to fetch IP ranges from
func (f *Fetcher) AddSource(source *FetchSource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sources[source.Name] = source
}

// Start begins fetching IP ranges periodically
func (f *Fetcher) Start(ctx context.Context) {
	ctx, f.cancel = context.WithCancel(ctx)

	f.mu.Lock()
	sources := make([]*FetchSource, 0, len(f.sources))
	for _, s := range f.sources {
		sources = append(sources, s)
	}
	f.mu.Unlock()

	// Initial fetch for all sources
	for _, source := range sources {
		f.fetchAndUpdate(ctx, source)
	}

	// Start periodic refresh for each source
	for _, source := range sources {
		go f.refreshLoop(ctx, source)
	}
}

// Stop stops all fetching
func (f *Fetcher) Stop() {
	if f.cancel != nil {
		f.cancel()
	}
}

func (f *Fetcher) refreshLoop(ctx context.Context, source *FetchSource) {
	interval := source.RefreshInterval
	if interval == 0 {
		interval = 24 * time.Hour
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.fetchAndUpdate(ctx, source)
		}
	}
}

func (f *Fetcher) fetchAndUpdate(ctx context.Context, source *FetchSource) {
	cidrs, err := f.fetch(ctx, source)
	if err != nil {
		f.logger.Error("failed to fetch IP ranges",
			"source", source.Name,
			"url", source.URL,
			"error", err,
		)
		metrics.RecordIPRangeFetchError(source.Name)
		return
	}

	// Get or create filter
	filter, exists := f.filters.Get(source.Name)
	if !exists {
		filter, err = NewFilter(source.Name, cidrs)
		if err != nil {
			f.logger.Error("failed to create filter",
				"source", source.Name,
				"error", err,
			)
			return
		}
		f.filters.Add(source.Name, filter)
	} else {
		if err := filter.Update(cidrs); err != nil {
			f.logger.Error("failed to update filter",
				"source", source.Name,
				"error", err,
			)
			return
		}
	}

	metrics.RecordIPRangesLoaded(source.Name, len(cidrs))
	f.logger.Info("updated IP ranges",
		"source", source.Name,
		"count", len(cidrs),
	)
}

func (f *Fetcher) fetch(ctx context.Context, source *FetchSource) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	return extractCIDRsWithJQ(body, source.JQQuery)
}

// extractCIDRsWithJQ extracts CIDRs from JSON using a jq query
func extractCIDRsWithJQ(data []byte, jqQuery string) ([]string, error) {
	// Parse JSON
	var input interface{}
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}

	// Parse jq query
	query, err := gojq.Parse(jqQuery)
	if err != nil {
		return nil, fmt.Errorf("parsing jq query: %w", err)
	}

	// Run query
	var cidrs []string
	iter := query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			return nil, fmt.Errorf("jq query error: %w", err)
		}
		if s, ok := v.(string); ok && s != "" {
			cidrs = append(cidrs, s)
		}
	}

	if len(cidrs) == 0 {
		return nil, fmt.Errorf("jq query returned no results")
	}

	return cidrs, nil
}
