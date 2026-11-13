// Package bridge implements the EasyBot Bridge protocol used by the original
// Minecraft server plugins. The wire format is JSON over WebSocket; every
// packet carries an integer "op" discriminator and the field names are
// snake_case, matching EasyBot.Bridge.Packet in the recovered sources.
package bridge

import (
	"encoding/json"
	"strings"
)

// OpCode mirrors EasyBot.Bridge.Packet.OpCode. The order is part of the wire
// protocol: the original serializes the enum as a number, so these values must
// never be reordered.
type OpCode int

const (
	OpHello           OpCode = iota // server -> client, first message on connect
	OpIdentify                       // client -> server, carries the server token
	OpHeartBeat                      // client -> server, periodic keep-alive
	OpIdentifySuccess                // server -> client, authentication accepted
	OpPacket                         // both ways, carries exec_op + callback_id
	OpCallBack                       // both ways, answer to a previous callback_id
)

// maxFrameSize bounds a single WebSocket message. The original plugins only
// send small JSON documents; chat lines are clipped well below this.
const maxFrameSize = 1 << 20

// HelloPacket is sent by the host immediately after the socket opens. Token is
// a per-connection nonce and is NOT the server token used for authentication.
type HelloPacket struct {
	Op        OpCode `json:"op"`
	Version   string `json:"version"`
	System    string `json:"system"`
	Dotnet    string `json:"dotnet"`
	SessionID string `json:"session_id"`
	Token     string `json:"token"`
	Interval  int    `json:"interval"`
}

// IdentifyPacket is the plugin's answer to HelloPacket. Token is the long-lived
// per-server token configured inside the Minecraft plugin.
type IdentifyPacket struct {
	Op                OpCode `json:"op"`
	Token             string `json:"token"`
	PluginVersion     string `json:"plugin_version"`
	ServerDescription string `json:"server_description"`
}

// IdentifySuccessPacket confirms authentication and tells the plugin which
// server record it was matched to.
type IdentifySuccessPacket struct {
	Op                   OpCode `json:"op"`
	ServerName           string `json:"server_name"`
	SupportGetPluginList bool   `json:"support_get_plugin_list"`
}

// HeartbeatPacket is a bare keep-alive marker.
type HeartbeatPacket struct {
	Op OpCode `json:"op"`
}

// PacketHeader is the minimal shape needed to route an op=OpPacket frame by its
// exec_op before decoding the full payload.
type PacketHeader struct {
	Op         OpCode `json:"op"`
	CallbackID string `json:"callback_id"`
	ExecOp     string `json:"exec_op"`
}

// PlayerInfo mirrors EasyBot.Bridge.Models.PlayerInfo. RawName keeps the
// display name with formatting codes intact.
type PlayerInfo struct {
	PlayerName    string `json:"player_name"`
	PlayerUUID    string `json:"player_uuid"`
	IP            string `json:"ip"`
	SkinURL       string `json:"skin_url"`
	Bedrock       bool   `json:"bedrock"`
	PlayerNameRaw string `json:"player_name_raw"`
}

// SyncMessagePacket is the chat event reported by the Minecraft plugin.
type SyncMessagePacket struct {
	Op         OpCode     `json:"op"`
	CallbackID string     `json:"callback_id"`
	ExecOp     string     `json:"exec_op"`
	Player     PlayerInfo `json:"player"`
	Message    string     `json:"message"`
	UseCommand bool       `json:"use_command"`
}

// SyncEnterExitMessagePacket reports a player joining or leaving.
type SyncEnterExitMessagePacket struct {
	Op         OpCode     `json:"op"`
	CallbackID string     `json:"callback_id"`
	ExecOp     string     `json:"exec_op"`
	Player     PlayerInfo `json:"player"`
	IsEnter    bool       `json:"is_enter"`
}

// SyncDeathMessagePacket reports a death message.
type SyncDeathMessagePacket struct {
	Op         OpCode     `json:"op"`
	CallbackID string     `json:"callback_id"`
	ExecOp     string     `json:"exec_op"`
	Player     PlayerInfo `json:"player"`
	Raw        string     `json:"raw"`
	Killer     string     `json:"killer"`
}

// ServerStatePacket reports the online player list. Players is a
// comma-separated string, matching the original packet.
type ServerStatePacket struct {
	Op      OpCode `json:"op"`
	Token   string `json:"token"`
	Players string `json:"players"`
}

// OutPacket is a host-initiated op=OpPacket frame with arbitrary payload.
type OutPacket struct {
	Op         OpCode `json:"op"`
	CallbackID string `json:"callback_id"`
	ExecOp     string `json:"exec_op"`
}

// SendToChatPacket makes the Minecraft plugin broadcast a line to every player.
type SendToChatPacket struct {
	Op         OpCode `json:"op"`
	CallbackID string `json:"callback_id"`
	ExecOp     string `json:"exec_op"`
	Text       string `json:"text"`
	Extra      []any  `json:"extra,omitempty"`
}

// RunCommandPacket asks the plugin to execute a command as a player or from the
// console when PlayerName is empty.
type RunCommandPacket struct {
	Op         OpCode `json:"op"`
	CallbackID string `json:"callback_id"`
	ExecOp     string `json:"exec_op"`
	PlayerName string `json:"player_name"`
	Command    string `json:"command"`
	EnablePapi bool   `json:"enable_papi"`
}

// exec_op values used by the Minecraft-to-host direction.
const (
	ExecSyncMessage          = "SYNC_MESSAGE"
	ExecSyncEnterExitMessage = "SYNC_ENTER_EXIT_MESSAGE"
	ExecSyncDeathMessage     = "SYNC_DEATH_MESSAGE"
	ExecPlayerJoin           = "PLAYER_JOIN"
	ExecReportPlayer         = "REPORT_PLAYER"
	ExecServerStateChanged   = "SERVER_STATE_CHANGED"
)

// exec_op values used by the host-to-Minecraft direction.
const (
	ExecSendToChat    = "SEND_TO_CHAT"
	ExecRunCommand    = "RUN_COMMAND"
	ExecGetServerInfo = "GET_SERVER_INFO"
)

// decodeHeader reads only the routing fields of an op=OpPacket frame.
func decodeHeader(raw []byte) (PacketHeader, error) {
	var header PacketHeader
	err := json.Unmarshal(raw, &header)
	return header, err
}

// clampChatLine trims control characters that would let a remote chat line
// forge a second line in the Minecraft console or the social platform.
func clampChatLine(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\x00", "")
	if len(value) > 2000 {
		value = value[:2000]
	}
	return value
}
