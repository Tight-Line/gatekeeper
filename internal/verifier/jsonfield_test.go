package verifier

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestJSONFieldVerifier_Verify(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		token   string
		body    string
		wantErr bool
	}{
		{
			name:    "top-level field match",
			path:    "clientState",
			token:   "secret123",
			body:    `{"clientState": "secret123", "other": "data"}`,
			wantErr: false,
		},
		{
			name:    "top-level field mismatch",
			path:    "clientState",
			token:   "secret123",
			body:    `{"clientState": "wrong", "other": "data"}`,
			wantErr: true,
		},
		{
			name:    "nested field match",
			path:    "data.clientState",
			token:   "secret123",
			body:    `{"data": {"clientState": "secret123"}}`,
			wantErr: false,
		},
		{
			name:    "array index field match",
			path:    "value.0.clientState",
			token:   "secret123",
			body:    `{"value": [{"clientState": "secret123", "type": "event"}]}`,
			wantErr: false,
		},
		{
			name:    "array second element",
			path:    "value.1.clientState",
			token:   "secret123",
			body:    `{"value": [{"clientState": "other"}, {"clientState": "secret123"}]}`,
			wantErr: false,
		},
		{
			name:    "microsoft graph format",
			path:    "value.0.clientState",
			token:   "mySecretClientState",
			body:    `{"value":[{"id":"abc","subscriptionId":"sub123","clientState":"mySecretClientState","changeType":"created","resource":"users/123/calendar/events/456"}]}`,
			wantErr: false,
		},
		{
			name:    "missing field",
			path:    "clientState",
			token:   "secret123",
			body:    `{"other": "data"}`,
			wantErr: true,
		},
		{
			name:    "missing nested field",
			path:    "data.clientState",
			token:   "secret123",
			body:    `{"data": {"other": "value"}}`,
			wantErr: true,
		},
		{
			name:    "array index out of bounds",
			path:    "value.5.clientState",
			token:   "secret123",
			body:    `{"value": [{"clientState": "secret123"}]}`,
			wantErr: true,
		},
		{
			name:    "empty body",
			path:    "clientState",
			token:   "secret123",
			body:    "",
			wantErr: true,
		},
		{
			name:    "invalid json",
			path:    "clientState",
			token:   "secret123",
			body:    "not json",
			wantErr: true,
		},
		{
			name:    "field is not string",
			path:    "count",
			token:   "123",
			body:    `{"count": 123}`,
			wantErr: true,
		},
		{
			name:    "deeply nested",
			path:    "a.b.c.d",
			token:   "found",
			body:    `{"a":{"b":{"c":{"d":"found"}}}}`,
			wantErr: false,
		},
		{
			name:    "array in middle of path",
			path:    "data.items.0.value",
			token:   "target",
			body:    `{"data":{"items":[{"value":"target"}]}}`,
			wantErr: false,
		},
		{
			name:    "null value at path",
			path:    "data.field",
			token:   "secret",
			body:    `{"data":{"field":null}}`,
			wantErr: true,
		},
		{
			name:    "null intermediate value",
			path:    "data.nested.field",
			token:   "secret",
			body:    `{"data":null}`,
			wantErr: true,
		},
		{
			name:    "json string auto-parse",
			path:    "value.0.clientState.secret",
			token:   "mySecret123",
			body:    `{"value":[{"clientState":"{\"secret\":\"mySecret123\",\"provider\":\"microsoft\"}"}]}`,
			wantErr: false,
		},
		{
			name:    "json string auto-parse mismatch",
			path:    "value.0.clientState.secret",
			token:   "mySecret123",
			body:    `{"value":[{"clientState":"{\"secret\":\"wrongSecret\",\"provider\":\"microsoft\"}"}]}`,
			wantErr: true,
		},
		{
			name:    "json string with nested array",
			path:    "data.items.0.name",
			token:   "first",
			body:    `{"data":"{\"items\":[{\"name\":\"first\"},{\"name\":\"second\"}]}"}`,
			wantErr: false,
		},
		{
			name:    "json string not parseable continues to fail",
			path:    "data.field.nested",
			token:   "value",
			body:    `{"data":{"field":"not json at all"}}`,
			wantErr: true,
		},
		{
			name:    "json string that is json but not navigable",
			path:    "data.field.nested",
			token:   "value",
			body:    `{"data":{"field":"\"just a string\""}}`,
			wantErr: true,
		},
		{
			name:    "microsoft graph realistic example",
			path:    "value.0.clientState.secret",
			token:   "webhook-secret-456",
			body:    `{"value":[{"subscriptionId":"abc","clientState":"{\"secret\":\"webhook-secret-456\",\"calendar_mapping\":123,\"provider\":\"microsoft\"}","changeType":"updated"}]}`,
			wantErr: false,
		},
		{
			name:    "json string is empty",
			path:    "data.nested.field",
			token:   "value",
			body:    `{"data":{"nested":""}}`,
			wantErr: true,
		},
		{
			name:    "json string is whitespace only",
			path:    "data.nested.field",
			token:   "value",
			body:    `{"data":{"nested":"   "}}`,
			wantErr: true,
		},
		{
			name:    "json string looks like json but is invalid",
			path:    "data.nested.field",
			token:   "value",
			body:    `{"data":{"nested":"{invalid json}"}}`,
			wantErr: true,
		},
		{
			name:    "json string parses but key missing",
			path:    "data.nested.missing",
			token:   "value",
			body:    `{"data":{"nested":"{\"other\":\"value\"}"}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewJSONFieldVerifier(tt.path, tt.token)
			req := httptest.NewRequest("POST", "/webhook", nil)
			err := v.Verify(req, []byte(tt.body))

			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestJSONFieldVerifier_Type(t *testing.T) {
	v := NewJSONFieldVerifier("path", "token")
	if v.Type() != "json_field" {
		t.Errorf("Type() = %q, want %q", v.Type(), "json_field")
	}
}

func TestExtractJSONPath(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		path    string
		want    any
		wantErr bool
	}{
		{
			name: "simple field",
			json: `{"foo": "bar"}`,
			path: "foo",
			want: "bar",
		},
		{
			name: "nested field",
			json: `{"a": {"b": "c"}}`,
			path: "a.b",
			want: "c",
		},
		{
			name: "array index",
			json: `{"arr": ["x", "y", "z"]}`,
			path: "arr.1",
			want: "y",
		},
		{
			name: "empty path returns root",
			json: `{"foo": "bar"}`,
			path: "",
			want: map[string]any{"foo": "bar"},
		},
		{
			name:    "missing key",
			json:    `{"foo": "bar"}`,
			path:    "baz",
			wantErr: true,
		},
		{
			name:    "index on non-array",
			json:    `{"foo": "bar"}`,
			path:    "foo.0",
			wantErr: true,
		},
		{
			name:    "non-numeric index on array",
			json:    `{"arr": [1, 2, 3]}`,
			path:    "arr.first",
			wantErr: true,
		},
		{
			name:    "negative index",
			json:    `{"arr": [1, 2, 3]}`,
			path:    "arr.-1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data any
			if err := jsonUnmarshal([]byte(tt.json), &data); err != nil {
				t.Fatalf("failed to parse test JSON: %v", err)
			}

			got, err := extractJSONPath(data, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			// Compare values (handle map comparison)
			switch want := tt.want.(type) {
			case map[string]any:
				gotMap, ok := got.(map[string]any)
				if !ok {
					t.Errorf("got %T, want map[string]any", got)
					return
				}
				if len(gotMap) != len(want) {
					t.Errorf("got map len %d, want %d", len(gotMap), len(want))
				}
			default:
				if got != want {
					t.Errorf("got %v, want %v", got, want)
				}
			}
		})
	}
}

// jsonUnmarshal is a helper for test readability
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
