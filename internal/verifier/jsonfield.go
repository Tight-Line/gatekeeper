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

		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("path %q not found: missing key %q", path, part)
			}
			current = val

		case []any:
			// Parse array index
			var idx int
			if _, err := fmt.Sscanf(part, "%d", &idx); err != nil {
				return nil, fmt.Errorf("path %q: expected array index, got %q", path, part)
			}
			if idx < 0 || idx >= len(v) {
				return nil, fmt.Errorf("path %q: index %d out of bounds (len=%d)", path, idx, len(v))
			}
			current = v[idx]

		default:
			return nil, fmt.Errorf("path %q: cannot traverse %T", path, current)
		}
	}

	return current, nil
}
