package onebot11

import (
	"encoding/json"
	"testing"
)

func TestParseQQConfigDefaultsAndValidation(t *testing.T) {
	c, err := ParseQQConfig(json.RawMessage(`{"AppID":"app","AppSecret":"secret"}`))
	if err != nil || c.APIBase != qqAPIBase || c.TokenURL != qqTokenURL || c.Intents == 0 || c.Heartbeat != 30000 {
		t.Fatalf("unexpected QQ config: %#v, %v", c, err)
	}
	if _, err := ParseQQConfig(json.RawMessage(`{"AppID":"","AppSecret":"secret"}`)); err == nil {
		t.Fatal("expected missing app id to be rejected")
	}
	if _, err := ParseQQConfig(json.RawMessage(`{"AppID":"app","AppSecret":"secret","HeartbeatInterval":10}`)); err != nil {
		t.Fatalf("invalid low heartbeat should use safe default: %v", err)
	}
}
