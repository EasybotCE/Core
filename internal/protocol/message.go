// SPDX-License-Identifier: AGPL-3.0-only
// Package protocol defines the experimental CE endpoint wire format.
// Transport and authenticated identity enforcement live in package session.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/EasybotCE/easybotce/internal/domain"
)

const (
	Version           = "0.1.0"
	MaxMessageBytes   = 64 << 10
	HelloType         = "session.hello"
	WelcomeType       = "session.welcome"
	ErrorType         = "session.error"
	PublishType       = "message.publish"
	DeliverType       = "message.deliver"
	AcceptedType      = "message.accepted"
	PublishCapability = "message.publish"
	ReceiveCapability = "message.receive"
)

var (
	identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	capability = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,127}$`)
	version    = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

type Message struct {
	Version string          `json:"v"`
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	ReplyTo string          `json:"reply_to,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

type Hello struct {
	EndpointID   string              `json:"endpoint_id"`
	EndpointType domain.EndpointType `json:"endpoint_type"`
	Versions     []string            `json:"versions"`
	Capabilities []string            `json:"capabilities"`
}

type Welcome struct {
	SessionID           string   `json:"session_id"`
	Version             string   `json:"version"`
	Capabilities        []string `json:"capabilities"`
	HeartbeatIntervalMS int      `json:"heartbeat_interval_ms"`
}

type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Publish sender fields are assertions of the authenticated endpoint, not Hub
// user authorization. source_endpoint_id is always assigned by the Hub.
type Publish struct {
	ChannelID  string `json:"channel_id"`
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name"`
	Text       string `json:"text"`
}
type Delivery struct {
	BindingID        string `json:"binding_id"`
	SourceEndpointID string `json:"source_endpoint_id"`
	SourceMessageID  string `json:"source_message_id"`
	SourceChannelID  string `json:"source_channel_id"`
	ChannelID        string `json:"channel_id"`
	SenderID         string `json:"sender_id"`
	SenderName       string `json:"sender_name"`
	Text             string `json:"text"`
}
type Accepted struct {
	Queued    int  `json:"queued"`
	Filtered  int  `json:"filtered"`
	Dropped   int  `json:"dropped"`
	Duplicate bool `json:"duplicate"`
}

func validName(s string) bool {
	return utf8.ValidString(s) && len([]rune(s)) >= 1 && len([]rune(s)) <= 128
}
func validText(s string) bool { return utf8.ValidString(s) && len([]rune(s)) <= 2048 }

func Decode(data []byte) (Message, error) {
	if len(data) > MaxMessageBytes {
		return Message{}, errors.New("message exceeds 64 KiB")
	}
	var msg Message
	if err := strictDecode(data, &msg); err != nil {
		return Message{}, fmt.Errorf("decode envelope: %w", err)
	}
	if err := msg.Validate(); err != nil {
		return Message{}, err
	}
	// Validate the wire shape as well as values: an optional field that is
	// explicitly null/empty is not equivalent to an omitted field in the schema.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Message{}, err
	}
	if raw, exists := fields["reply_to"]; exists && (msg.ReplyTo == "" || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))) {
		return Message{}, errors.New("reply_to must be a non-empty identifier when present")
	}
	return msg, nil
}

func (msg Message) Validate() error {
	if msg.Version != Version {
		return errors.New("unsupported envelope version")
	}
	if !identifier.MatchString(msg.ID) {
		return errors.New("invalid message id")
	}
	if msg.ReplyTo != "" && !identifier.MatchString(msg.ReplyTo) {
		return errors.New("invalid reply_to")
	}
	switch msg.Type {
	case PublishType:
		if msg.ReplyTo != "" {
			return errors.New("publish cannot have reply_to")
		}
		var p Publish
		if err := strictDecode(msg.Payload, &p); err != nil {
			return err
		}
		if !identifier.MatchString(p.ChannelID) || !identifier.MatchString(p.SenderID) || !validName(p.SenderName) || !validText(p.Text) || p.Text == "" {
			return errors.New("invalid published message")
		}
	case DeliverType:
		if msg.ReplyTo != "" {
			return errors.New("delivery cannot have reply_to")
		}
		var p Delivery
		if err := strictDecode(msg.Payload, &p); err != nil {
			return err
		}
		for _, id := range []string{p.BindingID, p.SourceEndpointID, p.SourceMessageID, p.SourceChannelID, p.ChannelID, p.SenderID} {
			if !identifier.MatchString(id) {
				return errors.New("invalid delivery identity")
			}
		}
		if !validName(p.SenderName) || !validText(p.Text) {
			return errors.New("invalid delivered message")
		}
	case AcceptedType:
		if msg.ReplyTo == "" {
			return errors.New("accepted requires reply_to")
		}
		var p Accepted
		if err := strictDecode(msg.Payload, &p); err != nil {
			return err
		}
		if p.Queued < 0 || p.Filtered < 0 || p.Dropped < 0 || p.Queued+p.Filtered+p.Dropped > 200 {
			return errors.New("invalid accepted counts")
		}
	case HelloType:
		if msg.ReplyTo != "" {
			return errors.New("hello must not have reply_to")
		}
		var payload Hello
		if err := strictDecode(msg.Payload, &payload); err != nil {
			return fmt.Errorf("decode hello: %w", err)
		}
		if !identifier.MatchString(payload.EndpointID) || !payload.EndpointType.Valid() {
			return errors.New("invalid endpoint identity")
		}
		if len(payload.Versions) == 0 || !uniqueMatches(payload.Versions, version) {
			return errors.New("versions must be a non-empty list of unique numeric semantic versions")
		}
		if payload.Capabilities == nil || !uniqueMatches(payload.Capabilities, capability) {
			return errors.New("capabilities must be a list of unique capability names")
		}
	case WelcomeType:
		if msg.ReplyTo == "" {
			return errors.New("welcome requires reply_to")
		}
		var payload Welcome
		if err := strictDecode(msg.Payload, &payload); err != nil {
			return fmt.Errorf("decode welcome: %w", err)
		}
		if !identifier.MatchString(payload.SessionID) || payload.Version != Version {
			return errors.New("invalid session or selected version")
		}
		if payload.Capabilities == nil || !uniqueMatches(payload.Capabilities, capability) {
			return errors.New("invalid selected capabilities")
		}
		if payload.HeartbeatIntervalMS < 1000 || payload.HeartbeatIntervalMS > 300000 {
			return errors.New("heartbeat_interval_ms must be between 1000 and 300000")
		}
	case ErrorType:
		var payload Failure
		if err := strictDecode(msg.Payload, &payload); err != nil {
			return fmt.Errorf("decode error: %w", err)
		}
		switch payload.Code {
		case "invalid_message", "unsupported_version", "permission_denied", "internal_error":
		default:
			return errors.New("invalid error code")
		}
		if len([]rune(payload.Message)) == 0 || len([]rune(payload.Message)) > 1024 {
			return errors.New("invalid error message length")
		}
	default:
		return errors.New("unknown message type")
	}
	return nil
}

// NegotiateVersion currently supports only the first experimental minor. It
// does not claim support for versions the implementation has never understood.
func NegotiateVersion(offered []string) (string, error) {
	for _, offeredVersion := range offered {
		if offeredVersion == Version {
			return Version, nil
		}
	}
	return "", errors.New("no mutually supported protocol version")
}

func uniqueMatches(values []string, pattern *regexp.Regexp) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !pattern.MatchString(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func strictDecode(data []byte, target any) error {
	if !utf8.Valid(data) {
		return errors.New("message must be valid UTF-8")
	}
	// encoding/json normally accepts case-insensitive struct keys and duplicate
	// members. Neither is acceptable for an unambiguous protocol contract.
	if err := checkObjectKeys(data, reflect.TypeOf(target).Elem()); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("expected an object, got null")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

// checkObjectKeys validates the exact keys of one envelope or payload. Payloads
// are checked separately by Validate; nested objects are not part of this draft.
func checkObjectKeys(data []byte, shape reflect.Type) error {
	allowed := make(map[string]bool, shape.NumField())
	required := make(map[string]bool, shape.NumField())
	for i := 0; i < shape.NumField(); i++ {
		name, _, _ := strings.Cut(shape.Field(i).Tag.Get("json"), ",")
		allowed[name] = true
		if !strings.Contains(shape.Field(i).Tag.Get("json"), ",omitempty") {
			required[name] = true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("expected a JSON object")
	}
	seen := make(map[string]bool, len(allowed))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("invalid object key")
		}
		key, ok := token.(string)
		if !ok || !allowed[key] {
			return errors.New("unknown object field")
		}
		if seen[key] {
			return errors.New("duplicate object field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("invalid field value")
		}
	}
	for name := range required {
		if !seen[name] {
			return errors.New("missing required field")
		}
	}
	if _, err := decoder.Token(); err != nil {
		return errors.New("invalid object ending")
	}
	return nil
}
