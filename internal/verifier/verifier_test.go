package verifier

import (
	"net/http"
	"strings"
	"testing"
)

// verifierTestCase represents a test case for verifier tests
type verifierTestCase struct {
	name      string
	setup     func() (*http.Request, []byte)
	wantErr   bool
	errString string
}

// runVerifierTests runs a set of test cases against a verifier
func runVerifierTests(t *testing.T, v Verifier, tests []verifierTestCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, body := tt.setup()
			err := v.Verify(req, body)
			assertVerifyResult(t, err, tt.wantErr, tt.errString)
		})
	}
}

// assertVerifyResult checks the verification result against expected values
func assertVerifyResult(t *testing.T, err error, wantErr bool, errString string) {
	t.Helper()
	if wantErr {
		if err == nil {
			t.Error("expected error, got nil")
		} else if errString != "" && !strings.Contains(err.Error(), errString) {
			t.Errorf("expected error containing %q, got %q", errString, err.Error())
		}
	} else {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

// assertVerifierType checks that a verifier returns the expected type
func assertVerifierType(t *testing.T, v Verifier, expectedType string) {
	t.Helper()
	if v.Type() != expectedType {
		t.Errorf("expected type %q, got %q", expectedType, v.Type())
	}
}
