package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/EasybotCE/easybotce/internal/store"
)

func TestPlayerPolicyAdminAndPluginEvaluation(t *testing.T) {
	f := runtimeForTest(t)
	f.request(t, "POST", "/v1/endpoints", `{"id":"survival","type":"mc-server","name":"survival","enabled":true}`, 201)
	credBytes := f.request(t, "POST", "/v1/endpoints/survival/credentials", `{"capabilities":["players.state"]}`, 201)
	var credential store.IssuedCredential
	if err := json.Unmarshal(credBytes, &credential); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/v1/player-policies", `{"id":"deny-player","endpoint_id":"survival","player_id":"uuid-1","mode":"deny","action":"ban","reason":"griefing","expires_at":0}`, 201)
	req, _ := http.NewRequest("POST", f.server.URL+"/plugin/v1/player-policy", strings.NewReader(`{"player_id":"uuid-1"}`))
	req.Header.Set("Authorization", "Bearer "+credential.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("plugin policy status=%d", response.StatusCode)
	}
	var decision struct {
		Allowed bool   `json:"allowed"`
		Action  string `json:"action"`
		Reason  string `json:"reason"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decision); err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Action != "ban" || decision.Reason != "griefing" {
		t.Fatalf("decision=%#v", decision)
	}
	audit := f.request(t, "GET", "/v1/audit/events", "", 200)
	if !strings.Contains(string(audit), "player_policy.create") {
		t.Fatalf("policy create missing from audit: %s", audit)
	}
	f.request(t, "DELETE", "/v1/player-policies/deny-player", "", 204)
}
