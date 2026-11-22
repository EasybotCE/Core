package onebot11

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"easybot/internal/store"
)

var ErrAdapterNotFound = errors.New("adapter not found")

type Manager struct {
	store    *store.Store
	ctx      context.Context
	cancel   context.CancelFunc
	onEvent  func(string, Event)
	mu       sync.RWMutex
	items    map[string]runtimeAdapter
	failures map[string]Status
}

func NewManager(parent context.Context, db *store.Store, onEvent func(string, Event)) *Manager {
	ctx, cancel := context.WithCancel(parent)
	return &Manager{store: db, ctx: ctx, cancel: cancel, onEvent: onEvent, items: make(map[string]runtimeAdapter), failures: make(map[string]Status)}
}

type runtimeAdapter interface {
	Start(context.Context) error
	Stop(context.Context) error
	Status() Status
	Send(context.Context, string, string, string) (string, error)
	Groups(context.Context) ([]map[string]any, error)
}

func (m *Manager) StartEnabled(ctx context.Context) error {
	items, err := m.store.List(ctx, "adapters")
	if err != nil {
		return err
	}
	for _, item := range items {
		adapterType, _, err := parseRecord(item)
		if err != nil {
			if item.Enabled {
				m.mu.Lock()
				m.failures[item.ID] = Status{State: StateFailed, LastError: safeMessage(err), LastErrorCode: errorCode(err)}
				m.mu.Unlock()
			}
			continue
		}
		if (adapterType != "Onebot11" && adapterType != "QQBot") || !item.Enabled {
			continue
		}
		if err := m.Start(ctx, item.ID); err != nil {
			continue
		}
	}
	return nil
}

func (m *Manager) Test(ctx context.Context, id string) (map[string]any, error) {
	adapterType, raw, err := m.loadRawConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if adapterType == "QQBot" {
		config, parseErr := ParseQQConfig(raw)
		if parseErr != nil {
			return nil, parseErr
		}
		return TestQQConnection(ctx, config)
	}
	config, parseErr := ParseConfig(raw)
	if parseErr != nil {
		return nil, parseErr
	}
	return TestConnection(ctx, config)
}

func (m *Manager) Start(ctx context.Context, id string) error {
	item, adapterType, raw, err := m.loadRawRecord(ctx, id)
	if err != nil {
		m.mu.Lock()
		m.failures[id] = Status{State: StateFailed, LastError: safeMessage(err), LastErrorCode: errorCode(err)}
		m.mu.Unlock()
		return err
	}
	if !item.Enabled {
		return fmt.Errorf("adapter is disabled")
	}
	var runtime runtimeAdapter
	if adapterType == "QQBot" {
		config, parseErr := ParseQQConfig(raw)
		if parseErr != nil {
			return parseErr
		}
		runtime = NewQQRuntime(id, config, func(event Event) {
			if m.onEvent != nil {
				m.onEvent(id, event)
			}
		})
	} else {
		config, parseErr := ParseConfig(raw)
		if parseErr != nil {
			return parseErr
		}
		runtime = NewRuntime(id, config, func(event Event) {
			if m.onEvent != nil {
				m.onEvent(id, event)
			}
		})
	}
	m.mu.Lock()
	if existing := m.items[id]; existing != nil {
		state := existing.Status().State
		if state == StateConnected || state == StateStarting || state == StateReconnecting {
			m.mu.Unlock()
			return nil
		}
	}
	m.items[id] = runtime
	delete(m.failures, id)
	m.mu.Unlock()
	return runtime.Start(m.ctx)
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	runtime := m.get(id)
	if runtime == nil {
		m.mu.Lock()
		delete(m.failures, id)
		m.mu.Unlock()
		return nil
	}
	return runtime.Stop(ctx)
}

func (m *Manager) Restart(ctx context.Context, id string) error {
	_ = m.Stop(ctx, id)
	return m.Start(ctx, id)
}

func (m *Manager) Status(id string) (Status, error) {
	if _, err := m.getRecord(context.Background(), id); err != nil {
		return Status{}, err
	}
	if runtime := m.get(id); runtime != nil {
		return runtime.Status(), nil
	}
	m.mu.RLock()
	failed, hasFailure := m.failures[id]
	m.mu.RUnlock()
	if hasFailure {
		return failed, nil
	}
	return Status{State: StateStopped}, nil
}

func (m *Manager) Capabilities(ctx context.Context, id string) ([]string, error) {
	adapterType, _, err := m.loadRawConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if adapterType == "QQBot" {
		return []string{"group_message_receive", "private_message_receive", "group_message_send", "private_message_send", "group_list", "heartbeat"}, nil
	}
	return append([]string(nil), Capabilities...), nil
}

func (m *Manager) Send(ctx context.Context, id, messageType, peerID, message string) (string, error) {
	runtime := m.get(id)
	if runtime == nil {
		return "", &APIError{Code: "adapter_offline", Message: "OneBot 适配器尚未启动", Retryable: true}
	}
	return runtime.Send(ctx, messageType, peerID, message)
}

func (m *Manager) Groups(ctx context.Context, id string) ([]map[string]any, error) {
	runtime := m.get(id)
	if runtime == nil {
		return nil, &APIError{Code: "adapter_offline", Message: "OneBot 适配器尚未启动", Retryable: true}
	}
	return runtime.Groups(ctx)
}

func (m *Manager) Shutdown(ctx context.Context) {
	m.cancel()
	m.mu.RLock()
	runtimes := make([]runtimeAdapter, 0, len(m.items))
	for _, runtime := range m.items {
		runtimes = append(runtimes, runtime)
	}
	m.mu.RUnlock()
	for _, runtime := range runtimes {
		_ = runtime.Stop(ctx)
	}
}

func (m *Manager) RuntimeCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}

func (m *Manager) get(id string) runtimeAdapter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.items[id]
}

func (m *Manager) loadConfig(ctx context.Context, id string) (store.Record, Config, error) {
	item, adapterType, raw, err := m.loadRawRecord(ctx, id)
	if err != nil {
		return store.Record{}, Config{}, err
	}
	if adapterType != "Onebot11" {
		return store.Record{}, Config{}, &APIError{Code: "adapter_type_unsupported", Message: "该适配器不是 OneBot 11", Retryable: false}
	}
	config, err := ParseConfig(raw)
	return item, config, err
}

func (m *Manager) loadRawConfig(ctx context.Context, id string) (string, json.RawMessage, error) {
	_, adapterType, raw, err := m.loadRawRecord(ctx, id)
	return adapterType, raw, err
}

func (m *Manager) loadRawRecord(ctx context.Context, id string) (store.Record, string, json.RawMessage, error) {
	item, err := m.getRecord(ctx, id)
	if err != nil {
		return store.Record{}, "", nil, err
	}
	adapterType, raw, err := parseRecord(item)
	if err != nil {
		return store.Record{}, "", nil, err
	}
	if adapterType != "Onebot11" && adapterType != "QQBot" {
		return store.Record{}, "", nil, &APIError{Code: "adapter_type_unsupported", Message: "当前运行时不支持该适配器", Retryable: false}
	}
	return item, adapterType, raw, nil
}

func (m *Manager) getRecord(ctx context.Context, id string) (store.Record, error) {
	items, err := m.store.List(ctx, "adapters")
	if err != nil {
		return store.Record{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return store.Record{}, ErrAdapterNotFound
}

func parseRecord(item store.Record) (string, json.RawMessage, error) {
	var detail struct {
		AdapterType string          `json:"adapterType"`
		Config      json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal([]byte(item.Detail), &detail); err != nil {
		return "", nil, &APIError{Code: "adapter_config_invalid", Message: "适配器配置格式无效", Retryable: false}
	}
	if detail.Config == nil {
		detail.Config = json.RawMessage(item.Detail)
	}
	return detail.AdapterType, detail.Config, nil
}

func (m *Manager) WaitForState(ctx context.Context, id string, state State) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if status, err := m.Status(id); err == nil && status.State == state {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
