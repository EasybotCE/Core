// SPDX-License-Identifier: AGPL-3.0-only
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The compiler must never resolve a reference through the filesystem/network.
// The official draft meta-schemas are bundled in the validation library.
type noExternalResources struct{}

func (noExternalResources) Load(string) (any, error) {
	return nil, errors.New("external schema resources are forbidden")
}

func compileProtocolSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(EndpointSchema))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(noExternalResources{})
	const location = "https://easybotce.invalid/endpoint.schema.json"
	if err := compiler.AddResource(location, doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("protocol", "examples", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// Compare both implementations for the tested data-model constraints. Wire
// limits, duplicate members and numeric spelling need separate codec tests.
func assertContract(t *testing.T, schema *jsonschema.Schema, value any, valid bool) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(instance); (err == nil) != valid {
		t.Fatalf("schema validity: want %v, error=%v", valid, err)
	}
	if _, err := protocol.Decode(data); (err == nil) != valid {
		t.Fatalf("codec validity: want %v, error=%v", valid, err)
	}
}

func TestProtocolSchemaExamplesAndRequiredFields(t *testing.T) {
	schema := compileProtocolSchema(t)
	required := map[string][]string{
		"hello":    {"endpoint_id", "endpoint_type", "versions", "capabilities"},
		"welcome":  {"session_id", "version", "capabilities", "heartbeat_interval_ms"},
		"error":    {"code", "message"},
		"publish":  {"channel_id", "sender_id", "sender_name", "text"},
		"deliver":  {"binding_id", "source_endpoint_id", "source_message_id", "source_channel_id", "channel_id", "sender_id", "sender_name", "text"},
		"accepted": {"queued", "filtered", "dropped", "duplicate"},
	}
	for name, payloadFields := range required {
		t.Run(name, func(t *testing.T) {
			assertContract(t, schema, fixture(t, name), true)
			fields := []string{"v", "id", "type", "payload"}
			if name == "welcome" || name == "accepted" {
				fields = append(fields, "reply_to")
			}
			for _, key := range fields {
				t.Run("missing "+key, func(t *testing.T) {
					value := fixture(t, name)
					delete(value, key)
					assertContract(t, schema, value, false)
				})
			}
			for _, key := range payloadFields {
				for _, missing := range []bool{true, false} {
					label := "null "
					if missing {
						label = "missing "
					}
					t.Run(label+key, func(t *testing.T) {
						value := fixture(t, name)
						payload := value["payload"].(map[string]any)
						if missing {
							delete(payload, key)
						} else {
							payload[key] = nil
						}
						assertContract(t, schema, value, false)
					})
				}
			}
			for _, payloadLevel := range []bool{true, false} {
				value := fixture(t, name)
				object := value
				if payloadLevel {
					object = value["payload"].(map[string]any)
				}
				object["unexpected"] = true
				assertContract(t, schema, value, false)
			}
		})
	}
}

func TestProtocolSchemaConstraints(t *testing.T) {
	schema := compileProtocolSchema(t)
	cases := []struct {
		name, example, key string
		payload            bool
		value              any
		valid              bool
	}{
		{"empty publish", "publish", "text", true, "", false},
		{"max publish", "publish", "text", true, strings.Repeat("中", 2048), true},
		{"oversized publish", "publish", "text", true, strings.Repeat("中", 2049), false},
		{"empty delivery", "deliver", "text", true, "", true},
		{"negative queue", "accepted", "queued", true, -1, false},
		{"oversized queue", "accepted", "queued", true, 201, false},
		{"fractional queue", "accepted", "queued", true, 1.5, false},
		{"publish reply", "publish", "reply_to", false, "x", false},
		{"delivery reply", "deliver", "reply_to", false, "x", false},
		{"wrong envelope version", "hello", "v", false, "9.0.0", false},
		{"unknown type", "hello", "type", false, "chat.message", false},
		{"empty id", "hello", "id", false, "", false},
		{"invalid id", "hello", "id", false, "bad id", false},
		{"non ascii id", "hello", "id", false, "服务器", false},
		{"maximum id", "hello", "id", false, strings.Repeat("a", 128), true},
		{"too long id", "hello", "id", false, strings.Repeat("a", 129), false},
		{"null payload", "hello", "payload", false, nil, false},
		{"hello reply", "hello", "reply_to", false, "hello-0", false},
		{"null reply", "error", "reply_to", false, nil, false},
		{"empty reply", "error", "reply_to", false, "", false},
		{"invalid reply", "welcome", "reply_to", false, "bad id", false},
		{"unknown endpoint", "hello", "endpoint_type", true, "server", false},
		{"chat bot endpoint", "hello", "endpoint_type", true, "chat-bot", true},
		{"plugin endpoint", "hello", "endpoint_type", true, "plugin", true},
		{"invalid endpoint id", "hello", "endpoint_id", true, "-bad", false},
		{"empty versions", "hello", "versions", true, []string{}, false},
		{"duplicate versions", "hello", "versions", true, []string{"0.1.0", "0.1.0"}, false},
		{"leading zero version", "hello", "versions", true, []string{"01.0.0"}, false},
		{"prerelease version", "hello", "versions", true, []string{"0.1.0-dev"}, false},
		{"offer other versions", "hello", "versions", true, []string{"0.1.0", "1.0.0"}, true},
		{"empty capabilities", "hello", "capabilities", true, []string{}, true},
		{"duplicate capabilities", "hello", "capabilities", true, []string{"chat.receive", "chat.receive"}, false},
		{"invalid capability", "hello", "capabilities", true, []string{"Chat.Receive"}, false},
		{"long capability", "hello", "capabilities", true, []string{strings.Repeat("a", 129)}, false},
		{"maximum capability", "hello", "capabilities", true, []string{strings.Repeat("a", 128)}, true},
		{"wrong selected version", "welcome", "version", true, "1.0.0", false},
		{"invalid session id", "welcome", "session_id", true, "", false},
		{"minimum heartbeat", "welcome", "heartbeat_interval_ms", true, 1000, true},
		{"maximum heartbeat", "welcome", "heartbeat_interval_ms", true, 300000, true},
		{"low heartbeat", "welcome", "heartbeat_interval_ms", true, 999, false},
		{"high heartbeat", "welcome", "heartbeat_interval_ms", true, 300001, false},
		{"fractional heartbeat", "welcome", "heartbeat_interval_ms", true, 1000.5, false},
		{"string heartbeat", "welcome", "heartbeat_interval_ms", true, "1000", false},
		{"unknown error code", "error", "code", true, "unknown", false},
		{"empty error", "error", "message", true, "", false},
		{"unicode error at limit", "error", "message", true, strings.Repeat("中", 1024), true},
		{"unicode error too long", "error", "message", true, strings.Repeat("中", 1025), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := fixture(t, tc.example)
			target := value
			if tc.payload {
				target = value["payload"].(map[string]any)
			}
			target[tc.key] = tc.value
			assertContract(t, schema, value, tc.valid)
		})
	}
	value := fixture(t, "error")
	delete(value, "reply_to")
	assertContract(t, schema, value, true)
	for _, value := range []any{nil, []any{}, "hello"} {
		assertContract(t, schema, value, false)
	}
}

func TestSchemaAndWireNumericSpelling(t *testing.T) {
	// JSON Schema's integer is a mathematical property. Go's integer decoder
	// additionally requires an integer literal. Keep this distinction explicit.
	schema := compileProtocolSchema(t)
	value := fixture(t, "welcome")
	value["payload"].(map[string]any)["heartbeat_interval_ms"] = json.Number("1000.0")
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.Decode(data); err == nil {
		t.Fatal("wire codec requires an integer literal")
	}
}
