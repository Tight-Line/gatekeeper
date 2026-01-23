package verifier

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/url"
)

// HeaderQueryParamVerifier verifies requests by parsing a header value as a query string
// and comparing a named parameter to a known token. This is useful for headers like
// Google's X-Goog-Channel-Token which can contain query string formatted data.
type HeaderQueryParamVerifier struct {
	header string
	name   string
	token  string
}

// NewHeaderQueryParamVerifier creates a new header query parameter verifier
func NewHeaderQueryParamVerifier(header, name, token string) *HeaderQueryParamVerifier {
	return &HeaderQueryParamVerifier{
		header: header,
		name:   name,
		token:  token,
	}
}

// Verify checks that the named parameter in the header's query string value matches the expected token
func (v *HeaderQueryParamVerifier) Verify(r *http.Request, _ []byte) error {
	headerValue := r.Header.Get(v.header)
	if headerValue == "" {
		return fmt.Errorf("%w: %s header missing", ErrSignatureEmpty, v.header)
	}

	// Parse the header value as a query string (key=value&key2=value2)
	values, err := url.ParseQuery(headerValue)
	if err != nil {
		return fmt.Errorf("%w: failed to parse %s header as query string", ErrSignatureMismatch, v.header)
	}

	value := values.Get(v.name)
	if value == "" {
		return fmt.Errorf("%w: %s parameter missing in %s header", ErrSignatureEmpty, v.name, v.header)
	}

	// Constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(value), []byte(v.token)) != 1 {
		return ErrTokenMismatch
	}

	return nil
}

// Type returns the verifier type
func (v *HeaderQueryParamVerifier) Type() string {
	return "header_query_param"
}
