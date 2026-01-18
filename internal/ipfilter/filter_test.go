package ipfilter

import (
	"testing"
)

func TestFilter_Allow(t *testing.T) {
	filter, err := NewFilter("test", []string{
		"10.0.0.0/8",
		"192.168.1.0/24",
		"172.16.0.1/32",
	})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	tests := []struct {
		ip      string
		allowed bool
	}{
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"192.168.1.1", true},
		{"192.168.1.254", true},
		{"192.168.2.1", false},
		{"172.16.0.1", true},
		{"172.16.0.2", false},
		{"8.8.8.8", false},
		{"127.0.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			got := filter.Allow(tt.ip)
			if got != tt.allowed {
				t.Errorf("Allow(%s) = %v, want %v", tt.ip, got, tt.allowed)
			}
		})
	}
}

func TestFilter_AllowFromAddr(t *testing.T) {
	filter, err := NewFilter("test", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	tests := []struct {
		addr    string
		allowed bool
	}{
		{"10.0.0.1:8080", true},
		{"10.0.0.1:443", true},
		{"192.168.1.1:8080", false},
		{"8.8.8.8:53", false},
		{"10.0.0.1", true}, // without port
		{"8.8.8.8", false}, // without port
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got := filter.AllowFromAddr(tt.addr)
			if got != tt.allowed {
				t.Errorf("AllowFromAddr(%s) = %v, want %v", tt.addr, got, tt.allowed)
			}
		})
	}
}

func TestFilter_InvalidCIDR(t *testing.T) {
	_, err := NewFilter("test", []string{"invalid-cidr"})
	if err == nil {
		t.Error("expected error for invalid CIDR")
	}
}

func TestFilter_Update(t *testing.T) {
	filter, err := NewFilter("test", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	// Initial state
	if !filter.Allow("10.0.0.1") {
		t.Error("expected 10.0.0.1 to be allowed initially")
	}
	if filter.Allow("192.168.1.1") {
		t.Error("expected 192.168.1.1 to be blocked initially")
	}

	// Update to new CIDRs
	err = filter.Update([]string{"192.168.0.0/16"})
	if err != nil {
		t.Fatalf("failed to update filter: %v", err)
	}

	// After update
	if filter.Allow("10.0.0.1") {
		t.Error("expected 10.0.0.1 to be blocked after update")
	}
	if !filter.Allow("192.168.1.1") {
		t.Error("expected 192.168.1.1 to be allowed after update")
	}
}

func TestFilterSet_Allow(t *testing.T) {
	fs := NewFilterSet()

	filter1, _ := NewFilter("aws", []string{"10.0.0.0/8"})
	filter2, _ := NewFilter("google", []string{"172.16.0.0/12"})

	fs.Add("aws", filter1)
	fs.Add("google", filter2)

	tests := []struct {
		name    string
		ip      string
		allowed bool
	}{
		{"aws", "10.0.0.1", true},
		{"aws", "172.16.0.1", false},
		{"google", "172.16.0.1", true},
		{"google", "10.0.0.1", false},
		{"nonexistent", "10.0.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.ip, func(t *testing.T) {
			got := fs.Allow(tt.name, tt.ip)
			if got != tt.allowed {
				t.Errorf("Allow(%s, %s) = %v, want %v", tt.name, tt.ip, got, tt.allowed)
			}
		})
	}
}

func TestFilter_Count(t *testing.T) {
	filter, err := NewFilter("test", []string{
		"10.0.0.0/8",
		"192.168.1.0/24",
		"172.16.0.1/32",
	})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	if filter.Count() != 3 {
		t.Errorf("expected count 3, got %d", filter.Count())
	}
}

func TestFilter_Name(t *testing.T) {
	filter, err := NewFilter("my-filter", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	if filter.Name() != "my-filter" {
		t.Errorf("expected name 'my-filter', got %q", filter.Name())
	}
}

func TestFilter_Allow_InvalidIP(t *testing.T) {
	filter, err := NewFilter("test", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	// Invalid IP should return false
	if filter.Allow("not-an-ip") {
		t.Error("expected invalid IP to return false")
	}
}

func TestFilter_Update_InvalidCIDR(t *testing.T) {
	filter, err := NewFilter("test", []string{"10.0.0.0/8"})
	if err != nil {
		t.Fatalf("failed to create filter: %v", err)
	}

	err = filter.Update([]string{"invalid-cidr"})
	if err == nil {
		t.Error("expected error for invalid CIDR in Update")
	}
}
