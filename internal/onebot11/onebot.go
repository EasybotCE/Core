// SPDX-License-Identifier: AGPL-3.0-only
// Package onebot11 contains the native OneBot 11 wire-format adapter.
package onebot11

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxEventBytes = 64 << 10

// MessageEvent is the normalized subset of a OneBot 11 message event that can
// enter EasybotCE's text routing pipeline. ChannelID is stable and namespaced.
type MessageEvent struct {
	MessageID string
	ChannelID string
	Platform  string
	UserID    string
	Nickname  string
	Text      string
}

type wireEvent struct {
	PostType    string          `json:"post_type"`
	SelfID      json.RawMessage `json:"self_id"`
	MessageType string          `json:"message_type"`
	SubType     string          `json:"sub_type"`
	MessageID   json.RawMessage `json:"message_id"`
	UserID      json.RawMessage `json:"user_id"`
	GroupID     json.RawMessage `json:"group_id"`
	Message     json.RawMessage `json:"message"`
	Sender      struct {
		Nickname string `json:"nickname"`
		Card     string `json:"card"`
	} `json:"sender"`
}

type segment struct {
	Type string `json:"type"`
	Data struct {
		Text string `json:"text"`
	} `json:"data"`
}

// ParseMessageEvent accepts group and private message events. Non-message
// events, self-sent echoes, non-text-only messages, malformed IDs, and oversized
// payloads are rejected so adapters cannot accidentally route arbitrary events.
func ParseMessageEvent(data []byte, selfID string) (MessageEvent, error) {
	if len(data) == 0 || len(data) > MaxEventBytes || !utf8.Valid(data) {
		return MessageEvent{}, errors.New("invalid OneBot event size or encoding")
	}
	var ev wireEvent
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&ev); err != nil {
		return MessageEvent{}, errors.New("invalid OneBot event")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return MessageEvent{}, errors.New("expected exactly one JSON value")
	}
	if ev.PostType != "message" || (ev.MessageType != "group" && ev.MessageType != "private") {
		return MessageEvent{}, errors.New("unsupported OneBot event")
	}
	userID, err := numericID(ev.UserID)
	effectiveSelfID := selfID
	if wireSelfID, idErr := numericID(ev.SelfID); idErr == nil {
		effectiveSelfID = wireSelfID
	}
	if err != nil || userID == "0" || (effectiveSelfID != "" && userID == effectiveSelfID) {
		return MessageEvent{}, errors.New("invalid or self-sent OneBot event")
	}
	messageID, err := numericID(ev.MessageID)
	if err != nil || messageID == "0" {
		return MessageEvent{}, errors.New("invalid OneBot message id")
	}
	channel := "private-" + userID
	if ev.MessageType == "group" {
		groupID, idErr := numericID(ev.GroupID)
		if idErr != nil || groupID == "0" {
			return MessageEvent{}, errors.New("invalid OneBot group id")
		}
		channel = "group-" + groupID
	}
	text, err := plainText(ev.Message)
	if err != nil || text == "" {
		return MessageEvent{}, errors.New("OneBot message has no supported text")
	}
	if len([]rune(text)) > 2048 {
		return MessageEvent{}, errors.New("OneBot message is too long")
	}
	nickname := ev.Sender.Card
	if nickname == "" {
		nickname = ev.Sender.Nickname
	}
	if nickname == "" {
		nickname = userID
	}
	if len([]rune(nickname)) > 128 {
		nickname = string([]rune(nickname)[:128])
	}
	platform := "group"
	if ev.MessageType == "private" {
		platform = "private"
	}
	return MessageEvent{MessageID: messageID, ChannelID: channel, Platform: platform, UserID: userID, Nickname: nickname, Text: text}, nil
}

func numericID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", errors.New("missing numeric id")
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		v, err := strconv.ParseUint(n.String(), 10, 64)
		if err != nil {
			return "", err
		}
		return strconv.FormatUint(v, 10), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || s == "" {
		return "", errors.New("invalid numeric id")
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(v, 10), nil
}

func plainText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", errors.New("missing message")
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		// String-form messages may contain CQ codes. Strip them instead of
		// treating protocol markup as text or executing non-text segments.
		return stripCQ(text), nil
	}
	var segments []segment
	if err := json.Unmarshal(raw, &segments); err != nil || len(segments) > 256 {
		return "", errors.New("invalid message segments")
	}
	var b strings.Builder
	for _, s := range segments {
		if s.Type == "text" {
			b.WriteString(s.Data.Text)
		}
	}
	return b.String(), nil
}

func stripCQ(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		start := strings.Index(s, "[CQ:")
		if start < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:start])
		end := strings.IndexByte(s[start:], ']')
		if end < 0 {
			b.WriteString(s[start:])
			break
		}
		s = s[start+end+1:]
	}
	return b.String()
}

// Action builds a OneBot 11 send action. Group/private identifiers are parsed
// as unsigned decimal integers and message text is encoded as a text segment.
func Action(platform, recipient, text, echo string) ([]byte, error) {
	id, err := strconv.ParseUint(recipient, 10, 64)
	if err != nil || id == 0 || (platform != "group" && platform != "private") || !utf8.ValidString(text) || text == "" || len([]rune(text)) > 2048 || echo == "" {
		return nil, errors.New("invalid OneBot action")
	}
	params := map[string]any{"message": []map[string]any{{"type": "text", "data": map[string]string{"text": text}}}}
	if platform == "group" {
		params["group_id"] = id
	} else {
		params["user_id"] = id
	}
	action := struct {
		Action string         `json:"action"`
		Params map[string]any `json:"params"`
		Echo   string         `json:"echo"`
	}{Action: "send_" + platform + "_msg", Params: params, Echo: echo}
	return json.Marshal(action)
}

// ActionResponse reports OneBot's result envelope without exposing arbitrary
// response data. A non-ok or non-zero retcode is an action failure.
func ActionResponse(data []byte, echo string) error {
	if len(data) == 0 || len(data) > 16<<10 || echo == "" {
		return errors.New("invalid OneBot action response")
	}
	var response struct {
		Status  string          `json:"status"`
		Retcode int             `json:"retcode"`
		Echo    json.RawMessage `json:"echo"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&response); err != nil {
		return errors.New("invalid OneBot action response")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid OneBot action response")
	}
	got, err := numericOrString(response.Echo)
	if err != nil || got != echo {
		return errors.New("OneBot action response echo mismatch")
	}
	switch response.Status {
	case "ok":
		if response.Retcode == 0 {
			return nil
		}
	case "async":
		// retcode 1 means the action was accepted for asynchronous execution;
		// this envelope is not a completion acknowledgement.
		if response.Retcode == 1 {
			return nil
		}
	}
	return fmt.Errorf("OneBot action failed (status %q, retcode %d)", response.Status, response.Retcode)
}

func numericOrString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("missing echo")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String(), nil
	}
	return "", errors.New("invalid echo")
}
