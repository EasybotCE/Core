package command

import (
	"encoding/json"
	"strings"
	"testing"

	"easybot/internal/store"
)

func raw(t *testing.T, c store.CommandConfig) string {
	t.Helper()
	v, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(v)
}

func TestPermissionMatrix(t *testing.T) {
	for _, test := range []struct {
		permission string
		ctx        Context
		allowed    bool
	}{
		{"all", Context{}, true},
		{"admin", Context{Verified: true, UserID: "u", Role: "admin"}, true},
		{"admin", Context{Verified: true, UserID: "u", Role: "owner"}, true},
		{"admin", Context{Verified: true, UserID: "u", Role: "member"}, false},
		{"admin", Context{Verified: false, UserID: "u", Role: "admin"}, false},
		{"admin", Context{Verified: true, UserID: "u", Role: "unknown"}, false},
		{"owner", Context{Verified: true, UserID: "u", Role: "owner"}, true},
		{"owner", Context{Verified: true, UserID: "u", Role: "admin"}, false},
		{"bound", Context{Verified: true, UserID: "u", Bound: true}, true},
		{"bound", Context{Verified: true, UserID: "u"}, false},
		{"bound", Context{Bound: true}, false},
		{"custom", Context{Verified: true, UserID: "u", Identities: []string{"operator"}}, true},
		{"custom", Context{Verified: true, UserID: "u", Identities: []string{"other"}}, false},
		{"custom", Context{Verified: false, UserID: "u", Identities: []string{"operator"}}, false},
		{"admin", Context{Verified: true, Role: "admin"}, false},
	} {
		t.Run(test.permission+"/"+test.ctx.Role, func(t *testing.T) {
			c := store.DefaultCommandConfig()
			c.Permission = test.permission
			if c.Permission == "custom" {
				c.Identity = "operator"
			}
			result, err := Simulate(raw(t, c), true, "/help", test.ctx)
			if err != nil || !result.Matched || result.Allowed != test.allowed || result.Executed || !result.Simulated || result.RuntimeAvailable {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !test.allowed && result.Reply != "" {
				t.Fatal("denied evaluation produced reply")
			}
		})
	}
}

func TestMatchingArgumentsAndLiteralExpansion(t *testing.T) {
	c := store.DefaultCommandConfig()
	c.Match = "regex"
	c.Triggers = []string{`echo (.+)`, `say (.+)`}
	c.Reply = "{sender_name}: {arg1} ({user_id})"
	ctx := Context{SenderName: "{arg1}", UserID: "u"}
	result, err := Simulate(raw(t, c), true, "/say $(whoami) {user_id}", ctx)
	if err != nil || result.Trigger != `say (.+)` || len(result.Arguments) != 1 || result.Reply != "{arg1}: $(whoami) {user_id} (u)" {
		t.Fatalf("%+v %v", result, err)
	}
	for _, message := range []string{"say hi", "/xxsay hi", "/say", "/say hi\nextra"} {
		result, err := Simulate(raw(t, c), true, message, ctx)
		if err != nil || result.Matched || result.Reply != "" {
			t.Fatalf("partial match %q: %+v %v", message, result, err)
		}
	}
	c = store.DefaultCommandConfig()
	for _, message := range []string{"help", "/HELP", "/help extra", " /help", "/help\n"} {
		result, _ := Simulate(raw(t, c), true, message, ctx)
		if result.Matched {
			t.Fatalf("unexpected exact match: %q", message)
		}
	}
	result, _ = Simulate(raw(t, c), false, "/help", ctx)
	if result.Matched || result.Allowed || result.Reason != "disabled" {
		t.Fatal(result)
	}
	if _, err := Simulate(raw(t, c), true, strings.Repeat("a", 8193), ctx); err == nil {
		t.Fatal("oversized input accepted")
	}
	c.Match = "regex"
	c.Triggers = []string{`(.+)`}
	c.Reply = strings.Repeat("{arg1}", 10)
	result, err = Simulate(raw(t, c), true, "/"+strings.Repeat("a", 2000), ctx)
	if err != nil || result.Allowed || result.Reply != "" || result.Reason != "reply_too_long" {
		t.Fatal(result, err)
	}
}
