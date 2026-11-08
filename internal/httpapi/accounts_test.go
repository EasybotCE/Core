// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"encoding/json"
	"github.com/EasybotCE/easybotce/internal/domain"
	"github.com/EasybotCE/easybotce/internal/store"
	"net/http"
	"strings"
	"testing"
)

func TestAccountBindingAndPlaceholderAPI(t *testing.T) {
	f := runtimeForTest(t)
	ctx := t.Context()
	mc := "mc-" + store.NewID()
	bot := "bot-" + store.NewID()
	if err := f.db.CreateEndpoint(ctx, store.Endpoint{ID: mc, Type: domain.MCServer, Name: "Lobby", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.CreateEndpoint(ctx, store.Endpoint{ID: bot, Type: domain.ChatBot, Name: "Chat", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	mcCred, err := f.db.IssueCredential(ctx, mc, []string{"accounts.challenge", "players.state"})
	if err != nil {
		t.Fatal(err)
	}
	botCred, err := f.db.IssueCredential(ctx, bot, []string{"accounts.bind"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(token, path, body string, want int) []byte {
		t.Helper()
		r, _ := http.NewRequest("POST", f.server.URL+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		resp, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			t.Fatalf("%s status %d want %d: %#v", path, resp.StatusCode, want, out)
		}
		b, _ := json.Marshal(out)
		return b
	}
	codeData := call(mcCred.Token, "/plugin/v1/accounts/challenge", `{"player_id":"uuid-1"}`, 200)
	var challenge struct {
		Code string `json:"code"`
	}
	if err = json.Unmarshal(codeData, &challenge); err != nil || challenge.Code == "" {
		t.Fatalf("challenge: %s %v", codeData, err)
	}
	call(botCred.Token, "/plugin/v1/accounts/claim", `{"code":"`+challenge.Code+`","platform_user_id":"user-1"}`, 201)
	call(mcCred.Token, "/plugin/v1/players/state", `{"player_id":"uuid-1","player_name":"Alex","online":true,"world":"world","x":1,"y":64,"z":-2}`, 200)
	resolved := call(mcCred.Token, "/plugin/v1/players/resolve", `{"player_id":"uuid-1"}`, 200)
	var result map[string]any
	if err = json.Unmarshal(resolved, &result); err != nil {
		t.Fatal(err)
	}
	placeholders := result["placeholders"].(map[string]any)
	if placeholders["account.platform_user_id"] != "user-1" || placeholders["player.online"] != true || placeholders["server.name"] != "Lobby" {
		t.Fatalf("placeholders: %#v", placeholders)
	}
	call(botCred.Token, "/plugin/v1/accounts/claim", `{"code":"`+challenge.Code+`","platform_user_id":"user-2"}`, 404)
}
