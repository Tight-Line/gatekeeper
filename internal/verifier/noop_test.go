package verifier

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNoopVerifier_Verify(t *testing.T) {
	v := NewNoopVerifier()

	body := []byte(`{"any":"data"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(string(body)))

	err := v.Verify(req, body)
	if err != nil {
		t.Errorf("noop verifier should always succeed, got error: %v", err)
	}
}

func TestNoopVerifier_Type(t *testing.T) {
	v := NewNoopVerifier()
	if v.Type() != "noop" {
		t.Errorf("expected type 'noop', got %q", v.Type())
	}
}
