// Package validator provides payload structure validation for webhooks.
// This is orthogonal to signature verification - it validates the shape of
// the payload against a known schema to detect malformed or malicious input.
package validator

import "errors"

// Common validation errors.
var (
	ErrValidationFailed = errors.New("payload validation failed")
	ErrSchemaNotFound   = errors.New("schema not found")
)

// Validator validates webhook payload structure.
type Validator interface {
	// Validate checks the payload against the configured schema.
	// Returns nil if the payload is valid, or an error describing the violation.
	Validate(payload []byte) error

	// Type returns the validator type name (e.g., "json_schema").
	Type() string
}
