// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EasybotCE/easybotce/internal/pipeline"
	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/EasybotCE/easybotce/internal/session"
	"github.com/EasybotCE/easybotce/internal/store"
	"github.com/coder/websocket"
	"net/http"
)

func TestSimulatorPublishesToRealHub(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := store.Open(ctx, "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := session.New(db)
	server := httptest.NewServer(m)
	defer server.Close()
	defer m.Close()
	tokens := map[string]string{}
	for _, id := range []string{"source", "target"} {
		if err := db.CreateEndpoint(ctx, store.Endpoint{ID: id, Type: "mc-server", Name: id, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		cred, err := db.IssueCredential(ctx, id, []string{protocol.PublishCapability, protocol.ReceiveCapability})
		if err != nil {
			t.Fatal(err)
		}
		tokens[id] = cred.Token
	}
	if err := db.CreateRuleGroup(ctx, store.RuleGroup{ID: "shared", Name: "Shared", Rules: pipeline.Rules{Transforms: []pipeline.Transform{}, Template: "{sender_name}: {text}"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRoutingBinding(ctx, store.RoutingBinding{ID: "route", SourceEndpointID: "source", TargetEndpointID: "target", SourceChannel: "chat", TargetChannel: "chat", RuleGroupID: "shared", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	address := "ws" + strings.TrimPrefix(server.URL, "http")
	target, _, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + tokens["target"]}}})
	if err != nil {
		t.Fatal(err)
	}
	defer target.CloseNow()
	payload, _ := json.Marshal(protocol.Hello{EndpointID: "target", EndpointType: "mc-server", Versions: []string{protocol.Version}, Capabilities: []string{protocol.ReceiveCapability}})
	raw, _ := json.Marshal(protocol.Message{Version: protocol.Version, ID: "h1", Type: protocol.HelloType, Payload: payload})
	if err := target.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := target.Read(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := connect(ctx, address, "source", tokens["source"], false, &protocol.Publish{ChannelID: "chat", SenderID: "player-1", SenderName: "Steve", Text: "hello"})
		done <- err
	}()
	_, raw, err = target.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := protocol.Decode(raw)
	if err != nil || msg.Type != protocol.DeliverType {
		t.Fatalf("delivery: %v", err)
	}
	var delivery protocol.Delivery
	_ = json.Unmarshal(msg.Payload, &delivery)
	if delivery.Text != "Steve: hello" || delivery.SourceEndpointID != "source" {
		t.Fatalf("bad delivery: %+v", delivery)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("simulator did not stop")
	}
}

func TestSimulatorRejectedCredential(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	db, err := store.Open(ctx, "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := session.New(db)
	server := httptest.NewServer(manager)
	defer server.Close()
	defer manager.Close()
	if err := run(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), "missing", "ce_"+strings.Repeat("a", 64), true, true, nil); err != errRejected {
		t.Fatalf("got %v", err)
	}
}
