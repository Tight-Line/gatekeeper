package verifier

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// JSONFieldVerifier verifies requests by checking a field value in the JSON body.
// This is useful for providers like Microsoft Graph that include a clientState
// token in the notification payload rather than in a header.
type JSONFieldVerifier struct {
	path  string // dot-notation path to the field (e.g., "value.0.clientState")
	token string // expected value
}

// NewJSONFieldVerifier creates a new JSON field verifier.
// The path uses dot notation to navigate the JSON structure:
//   - "clientState" - top-level field
//   - "value.0.clientState" - first element of "value" array, then "clientState" field
//   - "data.nested.field" - nested object traversal
func NewJSONFieldVerifier(path, token string) *JSONFieldVerifier {
	return &JSONFieldVerifier{
		path:  path,
		token: token,
	}
}

// Verify checks that the JSON body contains the expected token at the specified path.
func (v *JSONFieldVerifier) Verify(r *http.Request, body []byte) error {
	if len(body) == 0 {
		return ErrTokenMismatch
	}

	// Parse JSON into generic structure
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("%w: invalid JSON", ErrTokenMismatch)
	}

	// Extract value at path
	value, err := extractJSONPath(data, v.path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTokenMismatch, err)
	}

	// Compare as string
	strValue, ok := value.(string)
	if !ok {
		return fmt.Errorf("%w: field is not a string", ErrTokenMismatch)
	}

	if strValue != v.token {
		return ErrTokenMismatch
	}

	return nil
}

// Type returns the verifier type identifier.
func (v *JSONFieldVerifier) Type() string {
	return "json_field"
}

// extractJSONPath extracts a value from parsed JSON using dot notation.
// Supports object field access and array indexing:
//   - "field" -> data["field"]
//   - "field.nested" -> data["field"]["nested"]
//   - "array.0" -> data["array"][0]
//   - "array.0.field" -> data["array"][0]["field"]
//
// If a path segment cannot be traversed because the current value is a string,
// but that string contains valid JSON that IS traversable, the string is parsed
// and traversal continues. This allows paths like "value.0.clientState.secret"
// where clientState is a JSON-encoded string.
func extractJSONPath(data any, path string) (any, error) {
	if path == "" {
		return data, nil
	}

	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		if current == nil {
			return nil, fmt.Errorf("path %q not found: nil value", path)
		}

		navigated, err := navigatePart(current, part, path)
		if err == nil {
			current = navigated
			continue
		}

		// Navigation failed - if current is a JSON string, try parsing it
		str, ok := current.(string)
		if !ok {
			return nil, err
		}

		parsed, parseErr := tryParseJSONString(str)
		if parseErr != nil {
			return nil, err // Return original navigation error
		}

		// Try navigating the parsed JSON
		navigated, err = navigatePart(parsed, part, path)
		if err != nil {
			return nil, err
		}
		current = navigated
	}

	return current, nil
}

// navigatePart attempts to navigate one path segment on the current value.
func navigatePart(current any, part, fullPath string) (any, error) {
	switch v := current.(type) {
	case map[string]any:
		val, ok := v[part]
		if !ok {
			return nil, fmt.Errorf("path %q not found: missing key %q", fullPath, part)
		}
		return val, nil

	case []any:
		var idx int
		if _, err := fmt.Sscanf(part, "%d", &idx); err != nil {
			return nil, fmt.Errorf("path %q: expected array index, got %q", fullPath, part)
		}
		if idx < 0 || idx >= len(v) {
			return nil, fmt.Errorf("path %q: index %d out of bounds (len=%d)", fullPath, idx, len(v))
		}
		return v[idx], nil

	default:
		return nil, fmt.Errorf("path %q: cannot traverse %T", fullPath, current)
	}
}

// tryParseJSONString attempts to parse a string as JSON if it looks like JSON.
func tryParseJSONString(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty string")
	}
	// Only attempt parse if it looks like JSON object or array
	if s[0] != '{' && s[0] != '[' {
		return nil, fmt.Errorf("not JSON")
	}
	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}
