package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"easybot/internal/model"
)

// Proof binds a one-use server challenge to the configured server and time.
// TLS is still mandatory across untrusted networks: a MAC does not encrypt data.
func Proof(token, connectionID, nonce, serverID string, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(token))
	fmt.Fprintf(mac, "EasybotCE-bridge-v2\n%s\n%s\n%s\n%d", connectionID, nonce, serverID, timestamp)
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *Manager) authenticateIdentify(p IdentifyPacket, s *session, nonce string) (model.Record, string, error) {
	if p.ProtocolVersion != ProtocolVersion {
		return model.Record{}, "", fmt.Errorf("unsupported protocol version")
	}
	if p.ConnectionID != s.id || p.ServerID == "" || !freshTimestamp(p.Timestamp) {
		return model.Record{}, "", fmt.Errorf("invalid or expired challenge")
	}
	record, err := m.db.Get(context.Background(), "servers", p.ServerID)
	if err != nil || !record.Enabled {
		return model.Record{}, "", ErrUnknownServer
	}
	token := ServerToken(record)
	expected, _ := hex.DecodeString(Proof(token, s.id, nonce, record.ID, p.Timestamp))
	actual, err := hex.DecodeString(p.Proof)
	if token == "" || err != nil || !hmac.Equal(expected, actual) {
		return model.Record{}, "", ErrUnknownServer
	}
	s.protocolVersion = ProtocolVersion
	return record, token, nil
}

func freshTimestamp(timestamp int64) bool {
	delta := time.Now().Unix() - timestamp
	return delta >= -120 && delta <= 120
}

// validateEnvelope rejects stale sessions, out-of-order frames and replays.
func (s *session) validateEnvelope(raw []byte) bool {
	if s.protocolVersion != ProtocolVersion {
		return true
	}
	var e struct {
		ProtocolVersion int             `json:"protocol_version"`
		ConnectionID    string          `json:"connection_id"`
		ServerID        string          `json:"server_id"`
		RequestID       string          `json:"request_id"`
		Timestamp       int64           `json:"timestamp"`
		Sequence        uint64          `json:"sequence"`
		Type            string          `json:"type"`
		Payload         json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &e) != nil || e.ProtocolVersion != ProtocolVersion || e.ConnectionID != s.id || e.ServerID != s.serverID || e.RequestID == "" || len(e.RequestID) > 128 || e.Type == "" || len(e.Payload) == 0 || !freshTimestamp(e.Timestamp) || e.Sequence <= s.receivedSequence {
		return false
	}
	s.receivedSequence = e.Sequence
	return true
}

func tokensEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
