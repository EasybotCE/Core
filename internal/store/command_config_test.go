package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func contractJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCommandConfigValidation(t *testing.T) {
	for _, change := range []func(*CommandConfig){
		func(c *CommandConfig) { c.SchemaVersion = 0 },
		func(c *CommandConfig) { c.Action = "shell" },
		func(c *CommandConfig) { c.Action = "papi" },
		func(c *CommandConfig) { c.Prefix = "bad prefix" },
		func(c *CommandConfig) { c.Triggers = []string{} },
		func(c *CommandConfig) { c.Triggers = []string{"help", "help"} },
		func(c *CommandConfig) { c.Triggers = []string{"help extra"} },
		func(c *CommandConfig) { c.Match = "regex"; c.Triggers = []string{"("} },
		func(c *CommandConfig) { c.Match = "regex"; c.Triggers = []string{`(a)\1`} },
		func(c *CommandConfig) { c.Match = "regex"; c.Triggers = []string{`a)\z|(?:b`} },
		func(c *CommandConfig) { c.Match = "regex"; c.Triggers = []string{strings.Repeat("(a)", 10)} },
		func(c *CommandConfig) { c.Permission = "superuser" },
		func(c *CommandConfig) { c.Permission = "custom" },
		func(c *CommandConfig) { c.Identity = "unexpected" },
		func(c *CommandConfig) { c.Reply = "{arg1}" },
		func(c *CommandConfig) { c.Reply = "{sender_name" },
		func(c *CommandConfig) { c.Reply = "{unknown}" },
		func(c *CommandConfig) { c.Reply = "" },
	} {
		c := DefaultCommandConfig()
		change(&c)
		if _, err := ValidateCommandConfig(contractJSON(t, c)); err == nil {
			t.Fatalf("invalid config accepted: %+v", c)
		}
	}
	valid := contractJSON(t, DefaultCommandConfig())
	for _, value := range []string{"null", valid + " {}", strings.TrimSuffix(valid, "}") + `,"unknown":true}`} {
		if _, err := ValidateCommandConfig(value); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
}

func TestCommandSaveValidationAndLegacyPreservation(t *testing.T) {
	path := t.TempDir() + "/commands.db"
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	item, err := db.Save(ctx, Record{Kind: "commands", Name: "command", Detail: contractJSON(t, DefaultCommandConfig()), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	old := item.Detail
	item.Detail = `{"schemaVersion":1,"action":"shell"}`
	if _, err := db.Save(ctx, item); err == nil {
		t.Fatal("invalid config saved")
	}
	stored, err := db.Get(ctx, "commands", item.ID)
	if err != nil || stored.Detail != old {
		t.Fatal("failed save changed data")
	}
	// Emulate an existing pre-contract command without rewriting its source JSON.
	legacy := `{"mode":"text","custom":"preserve me"}`
	if _, err := db.db.Exec(db.query(`UPDATE records SET detail=? WHERE id=?`), legacy, item.ID); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stored, err = db.Get(ctx, "commands", item.ID)
	if err != nil || stored.Detail != legacy || !stored.Enabled {
		t.Fatal("startup changed legacy command")
	}
	if err := validateCommandRecord(stored); err == nil {
		t.Fatal("legacy command passed versioned validation")
	}
	stored.Detail = old
	if err := validateCommandRecord(stored); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Save(ctx, stored); err != nil {
		t.Fatal(err)
	}
}

func validateCommandRecord(item Record) error {
	_, err := ValidateCommandConfig(item.Detail)
	return err
}
