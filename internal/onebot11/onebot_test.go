// SPDX-License-Identifier: AGPL-3.0-only
package onebot11

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMessageEvent(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
		bad  bool
	}{
		{"group string with standard fields and CQ", `{"time":1,"self_id":99,"post_type":"message","message_type":"group","sub_type":"normal","message_id":12,"user_id":7,"group_id":8,"raw_message":"hi[CQ:at,qq=1]there","font":0,"message":"hi[CQ:at,qq=1]there","sender":{"nickname":"n","card":"c","role":"member"}}`, "hithere", false},
		{"private segments", `{"post_type":"message","message_type":"private","message_id":"13","user_id":"7","message":[{"type":"text","data":{"text":"hello"}},{"type":"image","data":{"file":"x"}},{"type":"text","data":{"text":"!"}}],"sender":{}}`, "hello!", false},
		{"self sent", `{"post_type":"message","message_type":"private","message_id":1,"user_id":99,"message":"no"}`, "", true},
		{"self id from event", `{"post_type":"message","self_id":99,"message_type":"private","message_id":1,"user_id":99,"message":"no"}`, "", true},
		{"not a message", `{"post_type":"notice","message_type":"group"}`, "", true},
		{"trailing value", `{"post_type":"message","message_type":"private","message_id":1,"user_id":2,"message":"x"} {}`, "", true},
		{"malformed", `{`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMessageEvent([]byte(tt.json), "99")
			if tt.bad {
				if err == nil {
					t.Fatalf("expected error; got %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Text != tt.want {
				t.Fatalf("text = %q, want %q", got.Text, tt.want)
			}
		})
	}
}

func TestParseMessageLimits(t *testing.T) {
	base := `{"post_type":"message","message_type":"private","message_id":1,"user_id":2,"message":"` + strings.Repeat("x", 2049) + `"}`
	if _, err := ParseMessageEvent([]byte(base), "99"); err == nil {
		t.Fatal("accepted oversized text")
	}
	if _, err := ParseMessageEvent([]byte(strings.Repeat(" ", MaxEventBytes+1)), "99"); err == nil {
		t.Fatal("accepted oversized event")
	}
}

func TestActionAndResponse(t *testing.T) {
	data, err := Action("group", "123", "hello", "request-1")
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Action string `json:"action"`
		Params struct {
			GroupID int       `json:"group_id"`
			Message []segment `json:"message"`
		} `json:"params"`
		Echo string `json:"echo"`
	}
	if err := json.Unmarshal(data, &action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_group_msg" || action.Params.GroupID != 123 || action.Echo != "request-1" || action.Params.Message[0].Data.Text != "hello" {
		t.Fatalf("unexpected action: %s", data)
	}
	for _, tc := range []struct {
		body    string
		wantErr bool
	}{
		{`{"status":"ok","retcode":0,"echo":"request-1"}`, false},
		{`{"status":"async","retcode":1,"echo":"request-1"}`, false},
		{`{"status":"failed","retcode":100,"echo":"request-1"}`, true},
		{`{"status":"ok","retcode":0,"echo":"other"}`, true},
		{`{"status":"async","retcode":0,"echo":"request-1"}`, true},
		{`{"status":"ok","retcode":0,"echo":"request-1"} {}`, true},
	} {
		err := ActionResponse([]byte(tc.body), "request-1")
		if (err != nil) != tc.wantErr {
			t.Errorf("response %s: err=%v", tc.body, err)
		}
	}
}

func TestActionRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{{"x", "1", "hello", "e"}, {"group", "0", "hello", "e"}, {"private", "1", "", "e"}, {"group", "1", "hello", ""}} {
		if _, err := Action(args[0], args[1], args[2], args[3]); err == nil {
			t.Errorf("accepted %#v", args)
		}
	}
}
