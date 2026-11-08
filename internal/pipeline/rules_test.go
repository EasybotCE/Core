// SPDX-License-Identifier: AGPL-3.0-only
package pipeline

import (
	"strings"
	"testing"
)

func TestPipeline(t *testing.T) {
	r := Rules{Include: "hello", Exclude: "secret", Transforms: []Transform{{"hello (.*)", "hi $1"}}, Template: "[{source_endpoint_id}] {sender_name}: {text}"}
	got, matched, err := r.Apply(Context{Text: "hello world", SenderName: "{text}", SourceEndpointID: "lobby"})
	if err != nil || !matched || got != "[lobby] {text}: hi world" {
		t.Fatalf("%q %v %v", got, matched, err)
	}
	for _, text := range []string{"bye", "hello secret"} {
		if _, ok, err := r.Apply(Context{Text: text}); err != nil || ok {
			t.Fatalf("filter: %v %v", ok, err)
		}
	}
	r = Rules{Transforms: []Transform{}}
	got, matched, err = r.Apply(Context{Text: "你好"})
	if err != nil || !matched || got != "你好" {
		t.Fatal(got, matched, err)
	}
}
func TestBounds(t *testing.T) {
	for _, r := range []Rules{{}, {Transforms: []Transform{{"[", ""}}}, {Transforms: []Transform{}, Include: strings.Repeat("x", 513)}, {Transforms: make([]Transform, 11)}} {
		if r.Validate() == nil {
			t.Fatal("accepted invalid rules")
		}
	}
	r := Rules{Transforms: []Transform{{"", strings.Repeat("x", 1024)}}}
	if _, _, err := r.Apply(Context{Text: strings.Repeat("a", 8192)}); err == nil {
		t.Fatal("unbounded transform")
	}
	r = Rules{Transforms: []Transform{}, Template: strings.Repeat("{text}", 500)}
	if _, _, err := r.Apply(Context{Text: strings.Repeat("a", 100)}); err == nil {
		t.Fatal("unbounded template")
	}
}
