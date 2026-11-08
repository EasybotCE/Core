// SPDX-License-Identifier: AGPL-3.0-only
package protocol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftExamples(t *testing.T) {
	files, err := filepath.Glob("../../api/protocol/examples/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 {
		t.Fatalf("want 6 examples, got %d", len(files))
	}
	for _, name := range files {
		t.Run(filepath.Base(name), func(t *testing.T) {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			msg, err := Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(msg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(encoded); err != nil {
				t.Fatalf("round trip: %v", err)
			}
		})
	}
}

func TestRejectMalformedMessages(t *testing.T) {
	data, err := os.ReadFile("../../api/protocol/examples/hello.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown version", func(m map[string]any) { m["v"] = "99.0.0" }},
		{"missing id", func(m map[string]any) { delete(m, "id") }},
		{"invalid id", func(m map[string]any) { m["id"] = "bad id" }},
		{"unknown type", func(m map[string]any) { m["type"] = "unknown.event" }},
		{"extra field", func(m map[string]any) { m["token"] = "must-not-be-on-wire" }},
		{"null payload", func(m map[string]any) { m["payload"] = nil }},
		{"null reply", func(m map[string]any) { m["reply_to"] = nil }},
		{"empty reply", func(m map[string]any) { m["reply_to"] = "" }},
		{"hello has reply", func(m map[string]any) { m["reply_to"] = "hello-0" }},
		{"unknown endpoint", func(m map[string]any) { m["payload"].(map[string]any)["endpoint_type"] = "unknown" }},
		{"missing capabilities", func(m map[string]any) { delete(m["payload"].(map[string]any), "capabilities") }},
		{"null capabilities", func(m map[string]any) { m["payload"].(map[string]any)["capabilities"] = nil }},
		{"repeated capability", func(m map[string]any) {
			m["payload"].(map[string]any)["capabilities"] = []string{"chat.receive", "chat.receive"}
		}},
		{"empty versions", func(m map[string]any) { m["payload"].(map[string]any)["versions"] = []string{} }},
		{"leading zero version", func(m map[string]any) { m["payload"].(map[string]any)["versions"] = []string{"01.0.0"} }},
		{"extra payload field", func(m map[string]any) { m["payload"].(map[string]any)["unexpected"] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			tc.mutate(value)
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(encoded); err == nil {
				t.Fatal("invalid message accepted")
			}
		})
	}
	for _, bad := range []string{"null", "{}", "[]", string(data) + " {}", strings.Repeat("x", MaxMessageBytes+1)} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Fatal("invalid raw message accepted")
		}
	}
}

func TestNegotiation(t *testing.T) {
	if got, err := NegotiateVersion([]string{"99.0.0", Version}); err != nil || got != Version {
		t.Fatalf("selection=%q err=%v", got, err)
	}
	for _, offer := range [][]string{nil, {}, {"0.0.0"}, {"1.0.0"}} {
		if _, err := NegotiateVersion(offer); err == nil {
			t.Fatal("negotiated an unsupported version")
		}
	}
}

func FuzzDecode(f *testing.F) {
	for _, name := range []string{"hello", "welcome", "error"} {
		data, err := os.ReadFile("../../api/protocol/examples/" + name + ".json")
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		msg, err := Decode(data)
		if err != nil {
			return
		}
		encoded, err := json.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode(encoded); err != nil {
			t.Fatalf("accepted message failed round trip: %v", err)
		}
	})
}

func TestRejectAmbiguousJSON(t *testing.T) {
	data, err := os.ReadFile("../../api/protocol/examples/hello.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(string(data), `"v":`, `"V":`, 1),
		strings.Replace(string(data), `"v":`, `"v":"0.1.0","v":`, 1),
		strings.Replace(string(data), `"endpoint_id":`, `"Endpoint_ID":`, 1),
		strings.Replace(string(data), `"endpoint_id":`, `"endpoint_id":"other","endpoint_id":`, 1),
		strings.Replace(string(data), "paper-lobby", string([]byte{0xff}), 1),
	} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Fatal("ambiguous or invalid UTF-8 JSON accepted")
		}
	}
}

func TestRejectInvalidWelcomeAndError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture string
		mutate  func(map[string]any)
	}{
		{"missing reply", "welcome", func(m map[string]any) { delete(m, "reply_to") }},
		{"null reply", "welcome", func(m map[string]any) { m["reply_to"] = nil }},
		{"bad session", "welcome", func(m map[string]any) { m["payload"].(map[string]any)["session_id"] = "bad id" }},
		{"bad selected version", "welcome", func(m map[string]any) { m["payload"].(map[string]any)["version"] = "9.0.0" }},
		{"null capabilities", "welcome", func(m map[string]any) { m["payload"].(map[string]any)["capabilities"] = nil }},
		{"heartbeat too small", "welcome", func(m map[string]any) { m["payload"].(map[string]any)["heartbeat_interval_ms"] = 999 }},
		{"heartbeat too large", "welcome", func(m map[string]any) { m["payload"].(map[string]any)["heartbeat_interval_ms"] = 300001 }},
		{"heartbeat fractional", "welcome", func(m map[string]any) { m["payload"].(map[string]any)["heartbeat_interval_ms"] = 1000.5 }},
		{"unknown code", "error", func(m map[string]any) { m["payload"].(map[string]any)["code"] = "unknown" }},
		{"empty message", "error", func(m map[string]any) { m["payload"].(map[string]any)["message"] = "" }},
		{"long message", "error", func(m map[string]any) { m["payload"].(map[string]any)["message"] = strings.Repeat("界", 1025) }},
		{"null message", "error", func(m map[string]any) { m["payload"].(map[string]any)["message"] = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("../../api/protocol/examples/" + tc.fixture + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			tc.mutate(value)
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(encoded); err == nil {
				t.Fatal("invalid message accepted")
			}
		})
	}
}
