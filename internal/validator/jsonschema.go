package validator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// JSONSchemaValidator validates payloads against JSON Schema.
type JSONSchemaValidator struct {
	schema *jsonschema.Schema
	name   string
	strict bool
}

// JSONSchemaConfig holds configuration for the JSON Schema validator.
type JSONSchemaConfig struct {
	// SchemaFile is the path to the JSON Schema file.
	SchemaFile string

	// SchemaContent is the inline JSON Schema content.
	// If both SchemaFile and SchemaContent are provided, SchemaContent takes precedence.
	SchemaContent string

	// Name is a descriptive name for this validator (used in error messages).
	Name string

	// Strict mode rejects payloads with additional properties not in the schema.
	// Default is false (permissive mode allows unknown properties).
	Strict bool
}

// NewJSONSchemaValidator creates a validator from a JSON Schema.
func NewJSONSchemaValidator(cfg JSONSchemaConfig) (*JSONSchemaValidator, error) {
	var schema *jsonschema.Schema
	var err error

	compiler := jsonschema.NewCompiler()

	switch {
	case cfg.SchemaContent != "":
		// Load from inline content
		if err := compiler.AddResource(cfg.Name, strings.NewReader(cfg.SchemaContent)); err != nil {
			return nil, fmt.Errorf("adding schema resource: %w", err)
		}
		schema, err = compiler.Compile(cfg.Name)
	case cfg.SchemaFile != "":
		// Load from file
		schema, err = compiler.Compile(cfg.SchemaFile)
	default:
		return nil, fmt.Errorf("either SchemaFile or SchemaContent must be provided")
	}

	if err != nil {
		return nil, fmt.Errorf("compiling schema: %w", err)
	}

	return &JSONSchemaValidator{
		schema: schema,
		name:   cfg.Name,
		strict: cfg.Strict,
	}, nil
}

// NewJSONSchemaValidatorFromFile creates a validator from a schema file path.
func NewJSONSchemaValidatorFromFile(path string) (*JSONSchemaValidator, error) {
	return NewJSONSchemaValidator(JSONSchemaConfig{
		SchemaFile: path,
		Name:       filepath.Base(path),
	})
}

// Validate checks the payload against the schema.
func (v *JSONSchemaValidator) Validate(payload []byte) error {
	var data any
	if err := json.Unmarshal(payload, &data); err != nil {
		return fmt.Errorf("%w: invalid JSON: %v", ErrValidationFailed, err)
	}

	if err := v.schema.Validate(data); err != nil {
		return fmt.Errorf("%w: %v", ErrValidationFailed, err)
	}

	return nil
}

// Type returns the validator type.
func (v *JSONSchemaValidator) Type() string {
	return "json_schema"
}

// LoadSchemaFromDir loads a schema from the schemas directory.
// The path is constructed as: {schemasDir}/{provider}/{schemaName}.json
func LoadSchemaFromDir(schemasDir, provider, schemaName string) (*JSONSchemaValidator, error) {
	path := filepath.Join(schemasDir, provider, schemaName+".json")

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s", ErrSchemaNotFound, path)
	}

	return NewJSONSchemaValidator(JSONSchemaConfig{
		SchemaFile: path,
		Name:       fmt.Sprintf("%s/%s", provider, schemaName),
	})
}
