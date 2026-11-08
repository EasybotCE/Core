// SPDX-License-Identifier: AGPL-3.0-only
package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EasybotCE/easybotce/internal/domain"
	"github.com/EasybotCE/easybotce/internal/pipeline"
	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/EasybotCE/easybotce/internal/store"
	"github.com/coder/websocket"
)

func TestOneBotReverseWebSocketAdmissionAndRouting(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	endpointID, targetID := store.NewID(), store.NewID()
	for _, ep := range []store.Endpoint{{ID: endpointID, Type: domain.ChatBot, Name: "source", Enabled: true}, {ID: targetID, Type: domain.ChatBot, Name: "target", Enabled: true}} {
		if err := db.CreateEndpoint(ctx, ep); err != nil {
			t.Fatal(err)
		}
	}
	issue := func(id string) store.IssuedCredential {
		c, err := db.IssueCredential(ctx, id, []string{protocol.PublishCapability, protocol.ReceiveCapability})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	sourceCredential, targetCredential := issue(endpointID), issue(targetID)
	ruleID := store.NewID()
	if err := db.CreateRuleGroup(ctx, store.RuleGroup{ID: ruleID, Name: "decorate", Rules: pipeline.Rules{Transforms: []pipeline.Transform{}, Template: "{sender_name}: {text}"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateRoutingBinding(ctx, store.RoutingBinding{ID: store.NewID(), SourceEndpointID: endpointID, TargetEndpointID: targetID, SourceChannel: "group-8", TargetChannel: "group-9", RuleGroupID: ruleID, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	manager := New(db)
	server := httptest.NewServer(http.HandlerFunc(manager.ServeOneBotHTTP))
	defer server.Close()
	defer manager.Close()
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	wsURL := "ws" + server.URL[len("http"):]
	for _, tc := range []struct {
		name, url string
		want      int
	}{
		{"missing bearer", wsURL, http.StatusUnauthorized},
		{"query string rejected", wsURL + "?access_token=" + sourceCredential.Token, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, response, err := websocket.Dial(dialCtx, tc.url, nil)
			if err == nil {
				t.Fatal("unexpected successful WebSocket admission")
			}
			if response == nil || response.StatusCode != tc.want {
				t.Fatalf("response = %#v, want HTTP %d (err %v)", response, tc.want, err)
			}
		})
	}
	connect := func(token string) *websocket.Conn {
		conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	source, target := connect(sourceCredential.Token), connect(targetCredential.Token)
	defer source.CloseNow()
	defer target.CloseNow()
	deadline := time.Now().Add(2 * time.Second)
	for len(manager.List()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := manager.List(); len(got) != 2 {
		t.Fatalf("expected two connected BOT endpoints, got %#v", got)
	}
	if _, response, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + sourceCredential.Token}}}); err == nil || response == nil || response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate endpoint admission: response=%#v err=%v, want HTTP 409", response, err)
	}
	if err := source.Write(dialCtx, websocket.MessageText, []byte(`{"time":1,"self_id":777,"post_type":"message","message_type":"group","message_id":1001,"user_id":42,"group_id":8,"message":"hello","sender":{"nickname":"player"}}`)); err != nil {
		t.Fatal(err)
	}
	readCtx, readCancel := context.WithTimeout(ctx, time.Second)
	defer readCancel()
	typ, actionBytes, err := target.Read(readCtx)
	if err != nil {
		t.Fatalf("read routed action: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("unexpected WebSocket message type %v", typ)
	}
	var action struct {
		Action string `json:"action"`
		Params struct {
			GroupID uint64 `json:"group_id"`
			Message []struct {
				Type string `json:"type"`
				Data struct {
					Text string `json:"text"`
				} `json:"data"`
			} `json:"message"`
		} `json:"params"`
		Echo string `json:"echo"`
	}
	if err := json.Unmarshal(actionBytes, &action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_group_msg" || action.Params.GroupID != 9 || len(action.Params.Message) != 1 || action.Params.Message[0].Data.Text != "player: hello" || action.Echo == "" {
		t.Fatalf("unexpected routed OneBot action: %s", actionBytes)
	}

	if err := target.Write(dialCtx, websocket.MessageText, []byte(`{"status":"async","retcode":0,"echo":"`+action.Echo+`"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if got, ok := manager.OneBotAction(action.Echo); !ok || got.Status == "failed" || got.Status == "succeeded" {
		t.Fatalf("async acknowledgement must not be terminal: %#v", got)
	}
	if err := target.Write(dialCtx, websocket.MessageText, []byte(`{"status":"ok","retcode":0,"echo":"`+action.Echo+`"}`)); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got, ok := manager.OneBotAction(action.Echo); ok && got.Status == "succeeded" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, ok := manager.OneBotAction(action.Echo); !ok || got.Status != "succeeded" || got.Retcode == nil || *got.Retcode != 0 {
		t.Fatalf("action final state = %#v, want succeeded/retcode=0", got)
	}
	restarted := New(db)
	if got, ok := restarted.OneBotAction(action.Echo); !ok || got.Status != "succeeded" || got.Retcode == nil || *got.Retcode != 0 {
		t.Fatalf("persisted action state = %#v, want succeeded/retcode=0", got)
	}

	// Revocation must tear down an already-authenticated reverse connection.
	if err := manager.Mutate(func() error { return db.RevokeCredential(ctx, endpointID, sourceCredential.ID) }, func(info Info) bool { return info.CredentialID == sourceCredential.ID }); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(time.Second)
	for len(manager.List()) != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := manager.List(); len(got) != 1 || got[0].EndpointID != targetID {
		t.Fatalf("source session remained active after credential revocation: %#v", got)
	}
}
