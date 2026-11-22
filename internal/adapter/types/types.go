package types

import (
	"encoding/json"
	"time"
)

type State string

const (
	StateStopped      State = "stopped"
	StateStarting     State = "starting"
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
	StateStopping     State = "stopping"
	StateFailed       State = "failed"
)

type Status struct {
	State          State      `json:"state"`
	ConnectedAt    *time.Time `json:"connectedAt,omitempty"`
	LastReceivedAt *time.Time `json:"lastReceivedAt,omitempty"`
	LastError      string     `json:"lastError,omitempty"`
	LastErrorCode  string     `json:"lastErrorCode,omitempty"`
	ReconnectCount uint64     `json:"reconnectCount"`
	Received       uint64     `json:"received"`
	Sent           uint64     `json:"sent"`
	SelfID         string     `json:"selfId,omitempty"`
	Nickname       string     `json:"nickname,omitempty"`
	ConnectionID   string     `json:"connectionId,omitempty"`
}

type Event struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	SubType       string          `json:"subType,omitempty"`
	MetaEventType string          `json:"metaEventType,omitempty"`
	NoticeType    string          `json:"noticeType,omitempty"`
	MessageID     string          `json:"messageId,omitempty"`
	MessageType   string          `json:"messageType,omitempty"`
	SelfID        string          `json:"selfId,omitempty"`
	PeerID        string          `json:"peerId,omitempty"`
	SenderID      string          `json:"senderId,omitempty"`
	SenderName    string          `json:"senderName,omitempty"`
	Content       string          `json:"content,omitempty"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Raw           json.RawMessage `json:"raw,omitempty"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *APIError) Error() string { return e.Message }
