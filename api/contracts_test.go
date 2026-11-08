// SPDX-License-Identifier: AGPL-3.0-only
package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// This checks local reference integrity, not full OpenAPI/JSON Schema semantics.
func TestContractDocumentsAndLocalReferences(t *testing.T) {
	for name, data := range map[string][]byte{"openapi": OpenAPI, "protocol": EndpointSchema} {
		t.Run(name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatal(err)
			}
			if name == "openapi" && root["openapi"] != "3.1.0" {
				t.Fatal("wrong OpenAPI version")
			}
			if name == "protocol" && root["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
				t.Fatal("wrong schema dialect")
			}
			var walk func(any)
			walk = func(node any) {
				switch value := node.(type) {
				case map[string]any:
					if ref, ok := value["$ref"].(string); ok {
						if !strings.HasPrefix(ref, "#/") {
							t.Fatalf("nonlocal reference %q", ref)
						}
						var target any = root
						for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
							object, ok := target.(map[string]any)
							if !ok {
								t.Fatalf("unresolvable reference %q", ref)
							}
							part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
							target, ok = object[part]
							if !ok {
								t.Fatalf("missing reference %q", ref)
							}
						}
					}
					for _, child := range value {
						walk(child)
					}
				case []any:
					for _, child := range value {
						walk(child)
					}
				}
			}
			walk(root)
		})
	}
}
