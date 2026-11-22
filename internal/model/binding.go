package model

import (
	"errors"
	"time"
)

var (
	ErrCrossPlatformConfirmationRequired = errors.New("cross-platform binding requires confirmation from an existing linked account")
	ErrBindingLimit                      = errors.New("social account has reached the four-player binding limit")
	ErrChallengeInvalid                  = errors.New("binding challenge is invalid or expired")
	ErrBindingConflict                   = errors.New("account is already bound")
	ErrBindingNotFound                   = errors.New("binding not found")
)

type Binding struct {
	ID           string    `json:"id"`
	PlayerUUID   string    `json:"playerUuid"`
	PlayerName   string    `json:"playerName,omitempty"`
	ServerID     string    `json:"serverId,omitempty"`
	PlatformType string    `json:"platformType"`
	AccountID    string    `json:"accountId"`
	DisplayName  string    `json:"displayName,omitempty"`
	BoundAt      time.Time `json:"boundAt"`
}

type BindingChallenge struct {
	Code       string    `json:"code"`
	PlayerUUID string    `json:"playerUuid"`
	PlayerName string    `json:"playerName,omitempty"`
	ServerID   string    `json:"serverId,omitempty"`
	GroupID    string    `json:"groupId,omitempty"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type BindingPlayerJoin struct {
	PlayerUUID     string
	PlayerName     string
	ServerID       string
	RequireBinding bool
	KickMessage    string
}
type BindingJoinResult struct {
	Kick        bool
	KickMessage string
	Challenge   *BindingChallenge
}
type BindingGroupMessage struct {
	PlatformType string
	AccountID    string
	DisplayName  string
	GroupID      string
	Text         string
}
type BindingMessageResult struct {
	Handled bool
	Message string
	Binding *Binding
}
