package config

// Test CIDR constants used in config_test.go.
// NOSONAR annotations acknowledge the go:S1313 hotspot at the single definition site.

const (
	testCIDRPrivate8 = "10.0.0.0/8" // NOSONAR - test fixture: RFC 1918 private CIDR /8
)
