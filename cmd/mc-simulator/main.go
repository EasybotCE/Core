// SPDX-License-Identifier: AGPL-3.0-only
// mc-simulator is a diagnostic CE client, not an MC server implementation or SDK.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/EasybotCE/easybotce/internal/store"
	"github.com/coder/websocket"
)

func main() {
	address := flag.String("url", "ws://127.0.0.1:8080/v1/gateway", "Hub gateway URL")
	id := flag.String("endpoint", "", "registered mc-server endpoint ID")
	once := flag.Bool("once", false, "exit after a validated welcome")
	reconnect := flag.Bool("reconnect", true, "retry transient disconnects with bounded exponential backoff")
	text := flag.String("publish", "", "publish one chat message after welcome (printed acknowledgements are queue receipts only)")
	channel := flag.String("channel", "chat", "source channel ID")
	sender := flag.String("sender", "player-1", "sender ID asserted by the simulator")
	senderName := flag.String("sender-name", "Steve", "sender display name")
	duration := flag.Duration("duration", 0, "stop after this duration; zero runs until interrupted")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(2)
	}
	token := os.Getenv("EASYBOTCE_ENDPOINT_TOKEN")
	parsed, err := url.Parse(*address)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" || !store.ValidID(*id) || !strings.HasPrefix(token, "ce_") || len(token) != 67 || *duration < 0 {
		fmt.Fprintln(os.Stderr, "valid gateway URL, endpoint ID and EASYBOTCE_ENDPOINT_TOKEN are required; URL credentials/query/fragment are forbidden")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *duration > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, *duration)
		defer stop()
	}
	var publish *protocol.Publish
	if *text != "" {
		if *once {
			fmt.Fprintln(os.Stderr, "-once cannot be combined with -publish")
			os.Exit(2)
		}
		publish = &protocol.Publish{ChannelID: *channel, SenderID: *sender, SenderName: *senderName, Text: *text}
		payload, _ := json.Marshal(publish)
		if err := (protocol.Message{Version: protocol.Version, ID: "validate", Type: protocol.PublishType, Payload: payload}).Validate(); err != nil {
			fmt.Fprintln(os.Stderr, "invalid publish fields")
			os.Exit(2)
		}
	}
	if err := run(ctx, *address, *id, token, *once, *reconnect, publish); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var errRejected = errors.New("Hub rejected the endpoint; check identity, credential and protocol version")

func run(ctx context.Context, address, id, token string, once, reconnect bool, publish *protocol.Publish) error {
	backoff := time.Second
	for {
		accepted, err := connect(ctx, address, id, token, once, publish)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			return nil
		}
		if errors.Is(err, errRejected) {
			return err
		}
		if !reconnect {
			return errors.New("gateway connection failed")
		}
		if accepted {
			// Never auto-repeat a possibly delivered publish on reconnect: dedup
			// is session-local, so replay could duplicate downstream chat.
			publish = nil
			backoff = time.Second
		}
		fmt.Fprintln(os.Stderr, "gateway disconnected; retrying")
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}
func connect(ctx context.Context, address, id, token string, once bool, publish *protocol.Publish) (bool, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, response, err := websocket.Dial(dialCtx, address, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
	cancel()
	if err != nil {
		if response != nil && (response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 400) {
			return false, errRejected
		}
		return false, errors.New("connection unavailable")
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.MaxMessageBytes)
	helloID := store.NewID()
	payload, _ := json.Marshal(protocol.Hello{EndpointID: id, EndpointType: "mc-server", Versions: []string{protocol.Version}, Capabilities: []string{protocol.PublishCapability, protocol.ReceiveCapability}})
	raw, _ := json.Marshal(protocol.Message{Version: protocol.Version, ID: helloID, Type: protocol.HelloType, Payload: payload})
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, 5*time.Second)
	defer handshakeCancel()
	if err := conn.Write(handshakeCtx, websocket.MessageText, raw); err != nil {
		return false, errors.New("cannot send hello")
	}
	typ, raw, err := conn.Read(handshakeCtx)
	if err != nil {
		return false, errors.New("cannot read welcome")
	}
	message, err := protocol.Decode(raw)
	if err != nil || typ != websocket.MessageText || message.Type != protocol.WelcomeType || message.ReplyTo != helloID {
		return false, errRejected
	}
	var welcome protocol.Welcome
	_ = json.Unmarshal(message.Payload, &welcome)
	if publish != nil {
		allowed := false
		for _, capability := range welcome.Capabilities {
			if capability == protocol.PublishCapability {
				allowed = true
			}
		}
		if !allowed {
			return true, errRejected
		}
	}
	fmt.Fprintln(os.Stdout, "session established")
	if once {
		_ = conn.Close(websocket.StatusNormalClosure, "simulation complete")
		return true, nil
	}
	if publish != nil {
		payload, _ := json.Marshal(publish)
		raw, _ := json.Marshal(protocol.Message{Version: protocol.Version, ID: store.NewID(), Type: protocol.PublishType, Payload: payload})
		writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		err := conn.Write(writeCtx, websocket.MessageText, raw)
		stop()
		if err != nil {
			return true, errors.New("publish outcome unknown; not replayed automatically")
		}
	}
	// Read must remain active so the library can answer RFC6455 ping frames.
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return true, errors.New("connection ended")
		}
		message, err := protocol.Decode(data)
		if kind != websocket.MessageText || err != nil {
			return true, errRejected
		}
		switch message.Type {
		case protocol.DeliverType, protocol.AcceptedType:
			// Intentionally prints message bodies: this diagnostic client is
			// interactive, unlike Hub logs (which never log chat by default).
			fmt.Fprintln(os.Stdout, string(data))
		case protocol.ErrorType:
			return true, errRejected
		default:
			return true, errRejected
		}
	}
}
