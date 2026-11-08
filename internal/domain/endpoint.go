// SPDX-License-Identifier: AGPL-3.0-only
// Package domain contains transport-independent concepts. Platform adapters map
// their native protocols into these concepts; they do not change wire formats.
package domain

type EndpointType string

const (
	MCServer EndpointType = "mc-server"
	ChatBot  EndpointType = "chat-bot"
	Plugin   EndpointType = "plugin"
)

func (kind EndpointType) Valid() bool {
	switch kind {
	case MCServer, ChatBot, Plugin:
		return true
	default:
		return false
	}
}
