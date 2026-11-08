// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"context"
	"encoding/json"
	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/EasybotCE/easybotce/internal/store"
	"github.com/coder/websocket"
	"testing"
)

const ruleBody = `{"id":"shared","name":"共享规则","rules":{"include":"hello","exclude":"secret","transforms":[{"pattern":"hello","replacement":"你好"}],"template":"[{source_endpoint_id}] {sender_name}: {text}"}}`

func bindingBody(id, source, target string) string {
	return `{"id":"` + id + `","source_endpoint_id":"` + source + `","target_endpoint_id":"` + target + `","source_channel":"chat","target_channel":"chat","rule_group_id":"shared","enabled":true}`
}
func wireWrite(t *testing.T, ctx context.Context, conn *websocket.Conn, id, kind string, payload any) {
	t.Helper()
	p, _ := json.Marshal(payload)
	raw, _ := json.Marshal(protocol.Message{Version: protocol.Version, ID: id, Type: kind, Payload: p})
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
}
func wireRead(t *testing.T, ctx context.Context, conn *websocket.Conn) protocol.Message {
	t.Helper()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := protocol.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v: %s", err, raw)
	}
	return msg
}
func messageConnection(t *testing.T, f *runtimeFixture, id string, caps []string) (*websocket.Conn, context.Context) {
	t.Helper()
	f.provision(t, id)
	raw, _ := json.Marshal(map[string]any{"capabilities": caps})
	issued := f.request(t, "POST", "/v1/endpoints/"+id+"/credentials", string(raw), 201)
	var c store.IssuedCredential
	if err := json.Unmarshal(issued, &c); err != nil {
		t.Fatal(err)
	}
	conn, ctx := f.dial(t, c.Token)
	wireWrite(t, ctx, conn, "hello", "session.hello", protocol.Hello{EndpointID: id, EndpointType: "mc-server", Versions: []string{protocol.Version}, Capabilities: []string{protocol.PublishCapability, protocol.ReceiveCapability, "admin.root"}})
	msg := wireRead(t, ctx, conn)
	var welcome protocol.Welcome
	if msg.Type != protocol.WelcomeType || json.Unmarshal(msg.Payload, &welcome) != nil || len(welcome.Capabilities) != len(caps) {
		t.Fatal("capability intersection", string(msg.Payload))
	}
	return conn, ctx
}
func readAck(t *testing.T, ctx context.Context, conn *websocket.Conn, id string) protocol.Accepted {
	t.Helper()
	msg := wireRead(t, ctx, conn)
	if msg.Type != protocol.AcceptedType || msg.ReplyTo != id {
		t.Fatalf("bad ack: %+v", msg)
	}
	var a protocol.Accepted
	if err := json.Unmarshal(msg.Payload, &a); err != nil {
		t.Fatal(err)
	}
	return a
}
func TestMessageRoutingAndDeduplication(t *testing.T) {
	f := runtimeForTest(t)
	a, ctx := messageConnection(t, f, "lobby", []string{protocol.PublishCapability, protocol.ReceiveCapability})
	b, bctx := messageConnection(t, f, "survival", []string{protocol.PublishCapability, protocol.ReceiveCapability})
	f.provision(t, "offline")
	f.request(t, "POST", "/v1/rule-groups", ruleBody, 201)
	f.request(t, "POST", "/v1/routing-bindings", bindingBody("to-survival", "lobby", "survival"), 201)
	f.request(t, "POST", "/v1/routing-bindings", bindingBody("to-lobby", "survival", "lobby"), 201)
	f.request(t, "POST", "/v1/routing-bindings", bindingBody("to-offline", "lobby", "offline"), 201)
	f.request(t, "DELETE", "/v1/rule-groups/shared", "", 409)
	pub := protocol.Publish{ChannelID: "chat", SenderID: "player-1", SenderName: "Steve", Text: "hello world"}
	wireWrite(t, ctx, a, "p1", protocol.PublishType, pub)
	ack := readAck(t, ctx, a, "p1")
	if ack.Queued != 1 || ack.Dropped != 1 || ack.Filtered != 0 || ack.Duplicate {
		t.Fatal(ack)
	}
	msg := wireRead(t, bctx, b)
	var d protocol.Delivery
	if msg.Type != protocol.DeliverType || json.Unmarshal(msg.Payload, &d) != nil || d.Text != "[lobby] Steve: 你好 world" || d.SourceEndpointID != "lobby" || d.SourceMessageID != "p1" {
		t.Fatal(string(msg.Payload))
	}
	wireWrite(t, ctx, a, "p1", protocol.PublishType, pub)
	ack = readAck(t, ctx, a, "p1")
	if !ack.Duplicate || ack.Queued != 1 {
		t.Fatal(ack)
	}
	// The next observed delivery must be the new ID, not a duplicate of p1.
	wireWrite(t, ctx, a, "p2", protocol.PublishType, pub)
	readAck(t, ctx, a, "p2")
	msg = wireRead(t, bctx, b)
	json.Unmarshal(msg.Payload, &d)
	if d.SourceMessageID != "p2" {
		t.Fatal("duplicate delivered")
	}
	pub.Text = "hello secret"
	wireWrite(t, ctx, a, "filtered", protocol.PublishType, pub)
	ack = readAck(t, ctx, a, "filtered")
	if ack.Filtered != 2 || ack.Queued != 0 || ack.Dropped != 0 {
		t.Fatal(ack)
	}
	pub.Text = "hello reverse"
	wireWrite(t, bctx, b, "reverse", protocol.PublishType, pub)
	ack = readAck(t, bctx, b, "reverse")
	if ack.Queued != 1 {
		t.Fatal(ack)
	}
	msg = wireRead(t, ctx, a)
	json.Unmarshal(msg.Payload, &d)
	if d.SourceEndpointID != "survival" {
		t.Fatal("reverse route")
	}
	// Rule edits apply without reconnecting either endpoint.
	f.request(t, "PUT", "/v1/rule-groups/shared", `{"id":"shared","name":"new","rules":{"include":"","exclude":"","transforms":[],"template":"changed: {text}"}}`, 200)
	wireWrite(t, ctx, a, "updated", protocol.PublishType, pub)
	readAck(t, ctx, a, "updated")
	msg = wireRead(t, bctx, b)
	json.Unmarshal(msg.Payload, &d)
	if d.Text != "changed: hello reverse" {
		t.Fatal(d)
	}
	// A conflicting retry is rejected instead of silently delivering twice.
	pub.Text = "different"
	wireWrite(t, ctx, a, "updated", protocol.PublishType, pub)
	msg = wireRead(t, ctx, a)
	if msg.Type != protocol.ErrorType {
		t.Fatal("conflicting ID accepted")
	}
}
func TestPublishRequiresCapability(t *testing.T) {
	f := runtimeForTest(t)
	c, ctx := messageConnection(t, f, "reader", []string{protocol.ReceiveCapability})
	wireWrite(t, ctx, c, "p1", protocol.PublishType, protocol.Publish{ChannelID: "chat", SenderID: "player", SenderName: "name", Text: "hello"})
	msg := wireRead(t, ctx, c)
	var failure protocol.Failure
	json.Unmarshal(msg.Payload, &failure)
	if msg.Type != protocol.ErrorType || failure.Code != "permission_denied" {
		t.Fatal(msg)
	}
}
func TestRoutingAPIValidationAndCRUD(t *testing.T) {
	f := runtimeForTest(t)
	f.provision(t, "a")
	f.provision(t, "b")
	for _, bad := range []string{
		`{"id":"shared","name":"x","rules":{"include":"","exclude":"","transforms":[],"template":null}}`,
		`{"id":"shared","name":"x","rules":{"include":"","exclude":"","transforms":[],"template":"","Template":"x"}}`,
		`{"id":"shared","name":"x","rules":{"include":"","exclude":"","transforms":[],"template":"","template":"x"}}`,
		`{"id":"shared","name":"x","rules":{"include":"[","exclude":"","transforms":[],"template":""}}`,
		`{"id":"shared","name":"x","rules":{"include":"","exclude":"","transforms":[{"pattern":"x","replacement":"y","hidden":"z"}],"template":""}}`,
	} {
		f.request(t, "POST", "/v1/rule-groups", bad, 400)
	}
	f.request(t, "POST", "/v1/rule-groups", ruleBody, 201)
	f.request(t, "POST", "/v1/rule-groups", ruleBody, 409)
	f.request(t, "GET", "/v1/rule-groups?limit=1", "", 200)
	f.request(t, "GET", "/v1/rule-groups?limit=1&limit=2", "", 400)
	f.request(t, "GET", "/v1/rule-groups/shared", "", 200)
	f.request(t, "POST", "/v1/routing-bindings", bindingBody("r", "a", "b"), 201)
	f.request(t, "POST", "/v1/routing-bindings", bindingBody("r", "a", "b"), 409)
	f.request(t, "POST", "/v1/routing-bindings", bindingBody("self", "a", "a"), 400)
	f.request(t, "POST", "/v1/routing-bindings", bindingBody("missing", "a", "missing"), 404)
	f.request(t, "PUT", "/v1/routing-bindings/r", bindingBody("other", "a", "b"), 400)
	f.request(t, "PUT", "/v1/routing-bindings/r", bindingBody("r", "b", "a"), 200)
	f.request(t, "GET", "/v1/routing-bindings?limit=1", "", 200)
	f.request(t, "HEAD", "/v1/routing-bindings/r", "", 200)
	f.request(t, "DELETE", "/v1/routing-bindings/r", "", 204)
	f.request(t, "DELETE", "/v1/rule-groups/shared", "", 204)
	f.request(t, "GET", "/v1/rule-groups/shared", "", 404)
}
