package httpapi

import (
	"encoding/json"
	"github.com/EasybotCE/easybotce/internal/store"
	"net/http"
	"strings"
	"testing"
)

func TestCommandRegistryAndPluginExecution(t *testing.T) {
	f := runtimeForTest(t)
	f.request(t, "POST", "/v1/endpoints", `{"id":"arena","type":"mc-server","name":"Arena","enabled":true}`, 201)
	bot := f.request(t, "POST", "/v1/endpoints", `{"id":"bot","type":"chat-bot","name":"Bot","enabled":true}`, 201)
	var c store.IssuedCredential
	if err := json.Unmarshal(bot, &c); err != nil {
		t.Fatal(err)
	}
	key := f.request(t, "POST", "/v1/endpoints/bot/credentials", `{"capabilities":["commands.execute"]}`, 201)
	if err := json.Unmarshal(key, &c); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/v1/commands", `{"id":"hello-cmd","endpoint_id":"arena","name":"hello","response":"Hi {{player}} on {{server}}: {{args}}","permission":"player","enabled":true}`, 201)
	req, _ := http.NewRequest("POST", f.server.URL+"/plugin/v1/commands/execute", strings.NewReader(`{"target_endpoint_id":"arena","name":"hello","player":"Alex","args":"there"}`))
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("execute status %d", resp.StatusCode)
	}
	var result struct {
		Response   string `json:"response"`
		Permission string `json:"permission"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Response != "Hi Alex on Arena: there" || result.Permission != "player" {
		t.Fatalf("execution result %#v", result)
	}
	f.request(t, "GET", "/v1/commands?endpoint_id=arena", "", 200)
	f.request(t, "DELETE", "/v1/commands/hello-cmd", "", 204)
}
