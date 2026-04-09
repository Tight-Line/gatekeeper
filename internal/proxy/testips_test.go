package proxy

// Test IP constants used throughout handler_test.go.
// Defining them as named constants satisfies the SonarCloud go:S1313
// "hardcoded IP address" rule by making the intent explicit.
// NOSONAR annotations on each definition acknowledge the single reviewed instance.

// CIDR ranges used in filter construction
const (
	testCIDRLoopback  = "127.0.0.0/8"    // NOSONAR - test fixture: loopback CIDR range
	testCIDRPrivate16 = "192.168.0.0/16" // NOSONAR - test fixture: RFC 1918 private CIDR
	testCIDRDocNet    = "203.0.113.0/24" // NOSONAR - test fixture: RFC 5737 documentation CIDR
)

// Public IPv4 addresses
const (
	testPublicIP  = "8.8.8.8"        // NOSONAR - test fixture: Google DNS public IP
	testPublicIP2 = "98.158.192.247" // NOSONAR - test fixture: arbitrary public IP
	testPublicIP3 = "1.1.1.1"        // NOSONAR - test fixture: Cloudflare DNS public IP
)

// RFC 5737 documentation IPv4 addresses (TEST-NET-2/3)
const (
	testDocIP1 = "203.0.113.50"  // NOSONAR - test fixture: RFC 5737 TEST-NET-3
	testDocIP2 = "198.51.100.25" // NOSONAR - test fixture: RFC 5737 TEST-NET-2
)

// RFC 1918 private IPv4 - 192.168.x.x range
const (
	testPrivateIP  = "192.168.1.100"   // NOSONAR - test fixture: RFC 1918 private IP
	testPrivateIP2 = "192.168.1.1"     // NOSONAR - test fixture: RFC 1918 private IP
	testPrivateIP3 = "192.168.0.1"     // NOSONAR - test fixture: RFC 1918 private IP
	testPrivateIP4 = "192.168.255.255" // NOSONAR - test fixture: RFC 1918 private IP
	testPrivateIP5 = "192.168.1.2"     // NOSONAR - test fixture: RFC 1918 private IP
)

// RFC 1918 private IPv4 - 10.x.x.x range
const (
	testPrivate10IP  = "10.0.0.1"       // NOSONAR - test fixture: RFC 1918 private IP
	testPrivate10IP2 = "10.0.0.5"       // NOSONAR - test fixture: RFC 1918 private IP
	testPrivate10IP3 = "10.10.0.5"      // NOSONAR - test fixture: RFC 1918 private IP
	testPrivate10IP4 = "10.255.255.255" // NOSONAR - test fixture: RFC 1918 private IP
)

// RFC 1918 private IPv4 - 172.16.x.x range
const (
	testPrivate172IP  = "172.16.0.1"     // NOSONAR - test fixture: RFC 1918 private IP
	testPrivate172IP2 = "172.31.255.255" // NOSONAR - test fixture: RFC 1918 private IP
)

// Link-local IPv4 addresses (169.254.0.0/16)
const (
	testLinkLocalIP  = "169.254.0.1"     // NOSONAR - test fixture: link-local IP
	testLinkLocalIP2 = "169.254.255.255" // NOSONAR - test fixture: link-local IP
	testLinkLocalIP3 = "169.254.1.1"     // NOSONAR - test fixture: link-local IP
)

// Loopback IPv4 addresses (127.0.0.0/8)
const (
	testLoopbackIP  = "127.0.0.1"       // NOSONAR - test fixture: IPv4 loopback
	testLoopbackIP2 = "127.255.255.255" // NOSONAR - test fixture: IPv4 loopback upper bound
)

// IPv6 addresses
const (
	testIPv6Loopback  = "::1"         // NOSONAR - test fixture: IPv6 loopback
	testIPv6LinkLocal = "fe80::1"     // NOSONAR - test fixture: IPv6 link-local
	testIPv6ULA       = "fd00::1"     // NOSONAR - test fixture: IPv6 unique local address
	testIPv6Public    = "2001:db8::1" // NOSONAR - test fixture: IPv6 documentation prefix
)
