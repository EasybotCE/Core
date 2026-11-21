package onebot11

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	qqAPIBase        = "https://api.sgroup.qq.com"
	qqTokenURL       = "https://bots.qq.com/app/getAppAccessToken"
	qqGatewayPath    = "/gateway"
	qqDefaultIntents = (1 << 0) | (1 << 9) | (1 << 25) | (1 << 30)
)

// QQConfig is the official QQ Bot credential and Gateway configuration.
type QQConfig struct {
	AppID      string `json:"AppID"`
	AppSecret  string `json:"AppSecret"`
	Intents    int    `json:"Intents"`
	APIBase    string `json:"APIBase"`
	GatewayURL string `json:"GatewayURL"`
	TokenURL   string `json:"TokenURL"`
	Heartbeat  int    `json:"HeartbeatInterval"`
}

func ParseQQConfig(raw json.RawMessage) (QQConfig, error) {
	c := QQConfig{APIBase: qqAPIBase, TokenURL: qqTokenURL, Heartbeat: 30000, Intents: qqDefaultIntents}
	if len(raw) != 0 && string(raw) != "null" && string(raw) != "{}" {
		if err := json.Unmarshal(raw, &c); err != nil {
			return QQConfig{}, &APIError{Code: "adapter_config_invalid", Message: "QQ Bot 配置不是有效 JSON"}
		}
	}
	if c.APIBase == "" {
		c.APIBase = qqAPIBase
	}
	if c.TokenURL == "" {
		c.TokenURL = qqTokenURL
	}
	if c.Intents == 0 {
		c.Intents = qqDefaultIntents
	}
	if c.AppID == "" || c.AppSecret == "" {
		var aliases struct {
			AppID     string `json:"appId"`
			AppSecret string `json:"appSecret"`
		}
		_ = json.Unmarshal(raw, &aliases)
		if c.AppID == "" {
			c.AppID = aliases.AppID
		}
		if c.AppSecret == "" {
			c.AppSecret = aliases.AppSecret
		}
	}
	if c.Heartbeat < 1000 || c.Heartbeat > 300000 {
		c.Heartbeat = 30000
	}
	if strings.TrimSpace(c.AppID) == "" || strings.TrimSpace(c.AppSecret) == "" {
		return QQConfig{}, &APIError{Code: "adapter_config_invalid", Message: "QQ Bot 必须配置 AppID 和 AppSecret"}
	}
	return c, nil
}

type qqToken struct {
	Code        int         `json:"code"`
	Message     string      `json:"message"`
	AccessToken string      `json:"access_token"`
	ExpiresIn   json.Number `json:"expires_in"`
}
type qqGateway struct {
	URL string `json:"url"`
}

type QQRuntime struct {
	id          string
	config      QQConfig
	onEvent     func(Event)
	mu          sync.RWMutex
	status      Status
	conn        *websocket.Conn
	cancel      context.CancelFunc
	done        chan struct{}
	writeMu     sync.Mutex
	seq         int64
	heartbeat   int
	token       string
	tokenExpiry time.Time
}

func NewQQRuntime(id string, config QQConfig, onEvent func(Event)) *QQRuntime {
	return &QQRuntime{id: id, config: config, onEvent: onEvent, status: Status{State: StateStopped}}
}

func (r *QQRuntime) Status() Status { r.mu.RLock(); defer r.mu.RUnlock(); return r.status }

func (r *QQRuntime) Start(parent context.Context) error {
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.done = make(chan struct{})
	r.status = Status{State: StateStarting, ConnectionID: newEcho()}
	r.mu.Unlock()
	go r.run(ctx)
	return nil
}

func (r *QQRuntime) Stop(ctx context.Context) error {
	r.mu.RLock()
	cancel, conn, done := r.cancel, r.conn, r.done
	r.mu.RUnlock()
	if cancel == nil {
		return nil
	}
	cancel()
	if conn != nil {
		_ = conn.Close()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *QQRuntime) run(ctx context.Context) {
	defer func() {
		r.mu.Lock()
		r.cancel = nil
		r.conn = nil
		r.status.State = StateStopped
		r.mu.Unlock()
		close(r.done)
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		if err := r.connect(ctx); err != nil {
			r.fail(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		err := r.readGateway(ctx)
		if ctx.Err() != nil {
			return
		}
		r.fail(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (r *QQRuntime) connect(ctx context.Context) error {
	token, err := r.accessToken(ctx)
	if err != nil {
		return err
	}
	url := r.config.GatewayURL
	if url == "" {
		var g qqGateway
		if err := r.request(ctx, http.MethodGet, r.config.APIBase+qqGatewayPath, nil, &g, token); err != nil {
			return err
		}
		url = g.URL
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return &APIError{Code: "adapter_network_error", Message: "无法连接 QQ Bot Gateway", Retryable: true}
	}
	r.mu.Lock()
	r.conn = conn
	r.status.State = StateStarting
	r.mu.Unlock()
	_, raw, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return &APIError{Code: "adapter_protocol_error", Message: "QQ Gateway Hello 读取失败", Retryable: true}
	}
	var hello struct {
		Op int `json:"op"`
		D  struct {
			HeartbeatInterval int `json:"heartbeat_interval"`
		} `json:"d"`
	}
	if json.Unmarshal(raw, &hello) != nil || hello.Op != 10 {
		conn.Close()
		return &APIError{Code: "adapter_protocol_error", Message: "QQ Gateway Hello 无效", Retryable: true}
	}
	interval := hello.D.HeartbeatInterval
	if interval < 1000 {
		interval = r.config.Heartbeat
	}
	r.mu.Lock()
	r.heartbeat = interval
	r.mu.Unlock()
	r.writeMu.Lock()
	err = conn.WriteJSON(map[string]any{"op": 2, "d": map[string]any{"token": "QQBot " + token, "intents": r.config.Intents, "shard": []int{0, 1}, "properties": map[string]string{"$os": "easybot", "$browser": "easybot", "$device": "easybot"}}})
	r.writeMu.Unlock()
	if err != nil {
		conn.Close()
		return err
	}
	r.mu.Lock()
	r.status.LastError = ""
	r.status.LastErrorCode = ""
	r.mu.Unlock()
	return nil
}

func (r *QQRuntime) readGateway(ctx context.Context) error {
	r.mu.RLock()
	conn := r.conn
	r.mu.RUnlock()
	r.mu.RLock()
	interval := r.heartbeat
	r.mu.RUnlock()
	if interval < 1000 {
		interval = r.config.Heartbeat
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()
	result := make(chan error, 1)
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				result <- err
				return
			}
			var p struct {
				Op int             `json:"op"`
				S  int64           `json:"s"`
				T  string          `json:"t"`
				D  json.RawMessage `json:"d"`
			}
			if json.Unmarshal(raw, &p) != nil {
				continue
			}
			if p.S > 0 {
				r.mu.Lock()
				r.seq = p.S
				r.mu.Unlock()
			}
			switch p.Op {
			case 0:
				r.dispatch(p.T, p.D)
			case 7:
				result <- fmt.Errorf("QQ Gateway 要求重连")
				return
			case 9:
				result <- fmt.Errorf("QQ Gateway 会话无效")
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-result:
			return &APIError{Code: "adapter_network_error", Message: "QQ Gateway 连接已断开", Retryable: true}
		case <-ticker.C:
			r.writeMu.Lock()
			err := conn.WriteJSON(map[string]any{"op": 1, "d": r.seq})
			r.writeMu.Unlock()
			if err != nil {
				return &APIError{Code: "adapter_network_error", Message: "QQ Bot 心跳发送失败", Retryable: true}
			}
		}
	}
}

func (r *QQRuntime) dispatch(kind string, raw json.RawMessage) {
	var d struct {
		ID          string `json:"id"`
		Content     string `json:"content"`
		ChannelID   string `json:"channel_id"`
		GuildID     string `json:"guild_id"`
		GroupOpenID string `json:"group_openid"`
		Author      struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"author"`
		OpenID string `json:"author_id"`
	}
	_ = json.Unmarshal(raw, &d)
	now := time.Now().UTC()
	e := Event{ID: d.ID, Type: kind, MessageID: d.ID, OccurredAt: now, Content: strings.TrimSpace(d.Content), Raw: append(json.RawMessage(nil), raw...)}
	switch kind {
	case "READY":
		var ready struct {
			SessionID string                        `json:"session_id"`
			User      struct{ ID, Username string } `json:"user"`
		}
		_ = json.Unmarshal(raw, &ready)
		r.mu.Lock()
		r.status.State = StateConnected
		r.status.ConnectedAt = &now
		r.status.SelfID = ready.User.ID
		r.status.Nickname = ready.User.Username
		r.status.ConnectionID = ready.SessionID
		r.mu.Unlock()
		return
	case "GROUP_AT_MESSAGE_CREATE":
		e.MessageType = "group"
		e.PeerID = d.GroupOpenID
		e.SenderID = d.OpenID
		e.SenderName = d.Author.Username
	case "C2C_MESSAGE_CREATE":
		e.MessageType = "private"
		e.PeerID = d.OpenID
		e.SenderID = d.OpenID
		e.SenderName = d.Author.Username
	case "AT_MESSAGE_CREATE", "MESSAGE_CREATE":
		e.MessageType = "group"
		e.PeerID = d.ChannelID
		e.SenderID = d.Author.ID
		e.SenderName = d.Author.Username
	default:
		return
	}
	r.mu.Lock()
	r.status.Received++
	r.status.LastReceivedAt = &now
	r.mu.Unlock()
	if e.ID == "" {
		e.ID = newEcho()
	}
	if r.onEvent != nil {
		r.onEvent(e)
	}
}

func (r *QQRuntime) Send(ctx context.Context, kind, peer, message string) (string, error) {
	path := r.config.APIBase
	body := map[string]any{"content": message}
	if kind == "group" {
		path += "/v2/groups/" + peer + "/messages"
	} else if kind == "private" {
		path += "/v2/users/" + peer + "/messages"
	} else {
		return "", &APIError{Code: "adapter_invalid_target", Message: "消息目标类型必须是 group 或 private"}
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := r.request(ctx, http.MethodPost, path, body, &out, ""); err != nil {
		return "", err
	}
	r.mu.Lock()
	r.status.Sent++
	r.mu.Unlock()
	return out.ID, nil
}
func (r *QQRuntime) Groups(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	if err := r.request(ctx, http.MethodGet, r.config.APIBase+"/users/@me/guilds", nil, &out, ""); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *QQRuntime) accessToken(ctx context.Context) (string, error) {
	r.mu.RLock()
	if r.token != "" && time.Now().Before(r.tokenExpiry) {
		t := r.token
		r.mu.RUnlock()
		return t, nil
	}
	r.mu.RUnlock()
	var out qqToken
	if err := r.request(ctx, http.MethodPost, r.config.TokenURL, map[string]string{"appId": r.config.AppID, "clientSecret": r.config.AppSecret}, &out, ""); err != nil {
		return "", err
	}
	if out.Code != 0 {
		return "", &APIError{Code: "adapter_auth_failed", Message: "QQ Bot token 获取失败: " + out.Message}
	}
	if out.AccessToken == "" {
		return "", &APIError{Code: "adapter_auth_failed", Message: "QQ Bot token 获取失败"}
	}
	seconds := int64(3600)
	if out.ExpiresIn != "" {
		if n, e := strconv.ParseInt(string(out.ExpiresIn), 10, 64); e == nil && n > 0 {
			seconds = n
		}
	}
	r.mu.Lock()
	r.token = out.AccessToken
	r.tokenExpiry = time.Now().Add(time.Duration(seconds-60) * time.Second)
	r.mu.Unlock()
	return out.AccessToken, nil
}
func (r *QQRuntime) request(ctx context.Context, method, url string, body any, out any, token string) error {
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token == "" {
		r.mu.RLock()
		token = r.token
		r.mu.RUnlock()
	}
	if token != "" {
		req.Header.Set("Authorization", "QQBot "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &APIError{Code: "adapter_network_error", Message: "QQ Bot API 请求失败", Retryable: true}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Code: "adapter_api_failed", Message: fmt.Sprintf("QQ Bot API 返回 HTTP %d", resp.StatusCode)}
	}
	if out != nil && len(data) > 0 && json.Unmarshal(data, out) != nil {
		return &APIError{Code: "adapter_protocol_error", Message: "QQ Bot API 响应无效"}
	}
	return nil
}
func (r *QQRuntime) fail(err error) {
	r.mu.Lock()
	r.status.State = StateReconnecting
	r.status.ReconnectCount++
	r.status.LastError = safeMessage(err)
	r.status.LastErrorCode = errorCode(err)
	r.mu.Unlock()
}

func TestQQConnection(ctx context.Context, config QQConfig) (map[string]any, error) {
	r := NewQQRuntime("test", config, nil)
	token, err := r.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var g qqGateway
	if config.GatewayURL != "" {
		g.URL = config.GatewayURL
	} else if err := r.request(ctx, http.MethodGet, config.APIBase+qqGatewayPath, nil, &g, token); err != nil {
		return nil, err
	}
	return map[string]any{"connected": true, "authenticated": true, "gateway": g.URL, "intents": config.Intents}, nil
}
