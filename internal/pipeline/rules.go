// SPDX-License-Identifier: AGPL-3.0-only
// Package pipeline implements bounded, deterministic text routing rules.
package pipeline

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const MaxText = 8192

type Transform struct {
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}
type Rules struct {
	Include    string      `json:"include"`
	Exclude    string      `json:"exclude"`
	Transforms []Transform `json:"transforms"`
	Template   string      `json:"template"`
}
type Context struct{ Text, SenderID, SenderName, SourceEndpointID, TargetEndpointID string }

func (r Rules) Validate() error {
	if r.Transforms == nil || len(r.Transforms) > 10 || !utf8.ValidString(r.Template) || len(r.Template) > 4096 {
		return errors.New("invalid rules")
	}
	patterns := []string{r.Include, r.Exclude}
	for _, t := range r.Transforms {
		patterns = append(patterns, t.Pattern)
		if !utf8.ValidString(t.Replacement) || len(t.Replacement) > 1024 {
			return errors.New("invalid replacement")
		}
	}
	for _, pattern := range patterns {
		if !utf8.ValidString(pattern) || len(pattern) > 512 {
			return errors.New("invalid pattern")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return errors.New("invalid pattern")
		}
	}
	return nil
}

// Apply uses RE2 regular expressions (no backtracking), a bounded number of
// transforms, and single-pass literal placeholder substitution. Empty template
// means {text}. Replacements use Go regexp's $1/${name} expansion semantics.
func (r Rules) Apply(c Context) (text string, matched bool, err error) {
	if err = r.Validate(); err != nil {
		return "", false, err
	}
	if !utf8.ValidString(c.Text) || len(c.Text) > MaxText {
		return "", false, errors.New("invalid text")
	}
	if r.Include != "" && !regexp.MustCompile(r.Include).MatchString(c.Text) {
		return "", false, nil
	}
	if r.Exclude != "" && regexp.MustCompile(r.Exclude).MatchString(c.Text) {
		return "", false, nil
	}
	text = c.Text
	for _, t := range r.Transforms {
		re := regexp.MustCompile(t.Pattern)
		// Append only bounded intermediate output; ReplaceAllString can otherwise
		// allocate megabytes for an empty-match regex with a large replacement.
		matches := re.FindAllStringSubmatchIndex(text, -1)
		var b strings.Builder
		at := 0
		for _, indexes := range matches {
			b.WriteString(text[at:indexes[0]])
			piece := re.ExpandString(nil, t.Replacement, text, indexes)
			if b.Len()+len(piece) > MaxText {
				return "", false, errors.New("transformed text too large")
			}
			b.Write(piece)
			at = indexes[1]
		}
		if b.Len()+len(text[at:]) > MaxText {
			return "", false, errors.New("transformed text too large")
		}
		b.WriteString(text[at:])
		text = b.String()
	}
	template := r.Template
	if template == "" {
		template = "{text}"
	}
	// Metadata and text are never recursively interpreted as templates.
	replacer := strings.NewReplacer("{text}", text, "{sender_id}", c.SenderID, "{sender_name}", c.SenderName, "{source_endpoint_id}", c.SourceEndpointID, "{target_endpoint_id}", c.TargetEndpointID)
	var output boundedText
	if _, err := replacer.WriteString(&output, template); err != nil {
		return "", false, err
	}
	return output.String(), true, nil
}

// boundedText refuses a write before retaining bytes beyond the render limit.
type boundedText struct{ strings.Builder }

func (b *boundedText) Write(p []byte) (int, error) {
	if len(p) > MaxText-b.Len() {
		return 0, errors.New("rendered text too large")
	}
	return b.Builder.Write(p)
}
func (b *boundedText) WriteString(p string) (int, error) {
	if len(p) > MaxText-b.Len() {
		return 0, errors.New("rendered text too large")
	}
	return b.Builder.WriteString(p)
}
