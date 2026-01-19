package validator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestJSONSchemaValidator_Validate(t *testing.T) {
	schema := `{
		"type": "object",
		"required": ["type", "event"],
		"properties": {
			"type": {"type": "string"},
			"event": {
				"type": "object",
				"required": ["id"],
				"properties": {
					"id": {"type": "string"},
					"data": {"type": "string"}
				}
			}
		}
	}`

	v, err := NewJSONSchemaValidator(JSONSchemaConfig{
		SchemaContent: schema,
		Name:          "test-schema",
	})
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{
			name:    "valid payload",
			payload: `{"type": "event_callback", "event": {"id": "E123"}}`,
			wantErr: false,
		},
		{
			name:    "valid payload with extra fields",
			payload: `{"type": "event_callback", "event": {"id": "E123", "data": "test"}, "extra": "allowed"}`,
			wantErr: false,
		},
		{
			name:    "missing required field type",
			payload: `{"event": {"id": "E123"}}`,
			wantErr: true,
		},
		{
			name:    "missing required field event",
			payload: `{"type": "event_callback"}`,
			wantErr: true,
		},
		{
			name:    "missing nested required field",
			payload: `{"type": "event_callback", "event": {}}`,
			wantErr: true,
		},
		{
			name:    "wrong type for field",
			payload: `{"type": 123, "event": {"id": "E123"}}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			payload: `{not valid json}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate([]byte(tt.payload))
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				} else if !errors.Is(err, ErrValidationFailed) {
					t.Errorf("expected ErrValidationFailed, got %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestJSONSchemaValidator_Type(t *testing.T) {
	v, err := NewJSONSchemaValidator(JSONSchemaConfig{
		SchemaContent: `{"type": "object"}`,
		Name:          "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	if v.Type() != "json_schema" {
		t.Errorf("expected type 'json_schema', got %q", v.Type())
	}
}

func TestNewJSONSchemaValidator_RequiresSchemaOrFile(t *testing.T) {
	_, err := NewJSONSchemaValidator(JSONSchemaConfig{
		Name: "test",
	})
	if err == nil {
		t.Error("expected error when neither SchemaFile nor SchemaContent provided")
	}
}

func TestNewJSONSchemaValidator_InvalidSchema(t *testing.T) {
	_, err := NewJSONSchemaValidator(JSONSchemaConfig{
		SchemaContent: `{not valid json}`,
		Name:          "test",
	})
	if err == nil {
		t.Error("expected error for invalid schema JSON")
	}
}

func TestNewJSONSchemaValidator_FileNotFound(t *testing.T) {
	_, err := NewJSONSchemaValidator(JSONSchemaConfig{
		SchemaFile: "/nonexistent/path/to/schema.json",
		Name:       "test",
	})
	if err == nil {
		t.Error("expected error for nonexistent schema file")
	}
}

func TestNewJSONSchemaValidatorFromFile(t *testing.T) {
	// Create a temporary schema file
	tmpDir := t.TempDir()
	schemaPath := filepath.Join(tmpDir, "test-schema.json")

	schema := `{
		"type": "object",
		"required": ["name"],
		"properties": {
			"name": {"type": "string"}
		}
	}`

	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}

	v, err := NewJSONSchemaValidatorFromFile(schemaPath)
	if err != nil {
		t.Fatalf("failed to create validator from file: %v", err)
	}

	// Test validation
	if err := v.Validate([]byte(`{"name": "test"}`)); err != nil {
		t.Errorf("valid payload failed: %v", err)
	}

	if v.Validate([]byte(`{}`)) == nil {
		t.Error("invalid payload should have failed")
	}
}

func TestLoadSchemaFromDir(t *testing.T) {
	// Create a temporary schemas directory
	tmpDir := t.TempDir()
	slackDir := filepath.Join(tmpDir, "slack")
	if err := os.MkdirAll(slackDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a test schema
	schema := `{
		"type": "object",
		"required": ["type"],
		"properties": {
			"type": {"type": "string"}
		}
	}`
	schemaPath := filepath.Join(slackDir, "event_callback.json")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatal(err)
	}

	// Test loading existing schema
	v, err := LoadSchemaFromDir(tmpDir, "slack", "event_callback")
	if err != nil {
		t.Fatalf("failed to load schema: %v", err)
	}

	if err := v.Validate([]byte(`{"type": "event_callback"}`)); err != nil {
		t.Errorf("valid payload failed: %v", err)
	}

	// Test loading non-existent schema
	_, err = LoadSchemaFromDir(tmpDir, "slack", "nonexistent")
	if !errors.Is(err, ErrSchemaNotFound) {
		t.Errorf("expected ErrSchemaNotFound, got %v", err)
	}
}
