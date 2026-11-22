package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"easybot/internal/model"
	"easybot/internal/runtime"
)

var (
	ErrLogLimit               = errors.New("limit must be between 1 and 500")
	ErrLogOffset              = errors.New("offset must be a non-negative integer")
	ErrLogSettingsInvalid     = errors.New("level must be debug/info/warn/error; retentionDays must be 1-3650 and maxRows must be 1000-10000000")
	ErrLogSettingsUnavailable = errors.New("log settings unavailable")
	ErrLogSettingsUpdate      = errors.New("log settings update failed")
)

type LogRepository interface {
	ListLogs(context.Context, model.LogFilter) ([]model.LogEntry, int, error)
	CountLogIDs(context.Context, []string) (int, error)
	GetLogSettings(context.Context) (model.LogSettings, error)
	SaveLogSettings(context.Context, model.LogSettings) error
}

type LogWriteController interface {
	Dropped() uint64
	Failed() uint64
	SetEnabled(bool)
}
type LogLevelWriteController interface{ SetMinLevel(string) }

type LogPage struct {
	Items  []model.LogEntry `json:"items"`
	Total  int              `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

type LogStatus struct {
	Persistent    bool      `json:"persistent"`
	RetentionDays int       `json:"retentionDays"`
	MaxRows       int       `json:"maxRows"`
	Level         string    `json:"level"`
	UpdatedAt     time.Time `json:"updatedAt"`
	DroppedWrites uint64    `json:"droppedWrites"`
	FailedWrites  uint64    `json:"failedWrites"`
}

// Logs coordinates persisted history, the runtime buffer and persistence settings.
type Logs struct {
	repository LogRepository
	logs       *runtime.Logs
	writer     LogWriteController
	settingsMu sync.Mutex
}

func NewLogs(repository LogRepository, logs *runtime.Logs, writer LogWriteController) *Logs {
	return &Logs{repository: repository, logs: logs, writer: writer}
}

// List preserves the history contract: pending buffer entries supplement the
// first page only; subsequent pages retain database offsets. Persisted IDs are
// counted separately to avoid inflating total when an entry is outside that page.
func (s *Logs) List(ctx context.Context, filter model.LogFilter) (LogPage, error) {
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	if filter.Limit < 1 || filter.Limit > 500 {
		return LogPage{}, ErrLogLimit
	}
	if filter.Offset < 0 {
		return LogPage{}, ErrLogOffset
	}
	filter.Query = runtime.NormalizeLogFilterText(filter.Query)
	items, total, err := s.repository.ListLogs(ctx, filter)
	if err != nil {
		return LogPage{}, err
	}
	if filter.Offset == 0 {
		seen := make(map[string]bool, len(items))
		memoryOrder := make(map[string]int)
		memoryOnly := make([]string, 0)
		for _, item := range items {
			seen[item.ID] = true
		}
		for index, item := range s.logs.Recent(500) {
			memoryOrder[item.ID] = index
			if seen[item.ID] || !matchesLog(item, filter) {
				continue
			}
			items = append(items, item)
			seen[item.ID] = true
			memoryOnly = append(memoryOnly, item.ID)
		}
		sort.Slice(items, func(i, j int) bool {
			if !items[i].Time.Equal(items[j].Time) {
				return items[i].Time.After(items[j].Time)
			}
			left, leftMemory := memoryOrder[items[i].ID]
			right, rightMemory := memoryOrder[items[j].ID]
			if leftMemory && rightMemory {
				return left < right
			}
			if leftMemory != rightMemory {
				return leftMemory
			}
			return items[i].ID > items[j].ID
		})
		if len(items) > filter.Limit {
			items = items[:filter.Limit]
		}
		persistedMemory, err := s.repository.CountLogIDs(ctx, memoryOnly)
		if err != nil {
			return LogPage{}, err
		}
		total += len(memoryOnly) - persistedMemory
	}
	return LogPage{Items: items, Total: total, Limit: filter.Limit, Offset: filter.Offset}, nil
}

func matchesLog(item model.LogEntry, filter model.LogFilter) bool {
	return (filter.Level == "" || item.Level == filter.Level) &&
		(filter.Component == "" || item.Source == filter.Component) &&
		(filter.RequestID == "" || item.RequestID == filter.RequestID) &&
		(filter.Query == "" || strings.Contains(strings.ToLower(item.Message), strings.ToLower(filter.Query))) &&
		(filter.From == nil || !item.Time.Before(*filter.From)) &&
		(filter.To == nil || item.Time.Before(*filter.To))
}

func (s *Logs) Status(ctx context.Context) (LogStatus, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings, err := s.repository.GetLogSettings(ctx)
	if err != nil {
		return LogStatus{}, fmt.Errorf("%w: %w", ErrLogSettingsUnavailable, err)
	}
	status := LogStatus{Level: settings.Level, Persistent: settings.Persistent, RetentionDays: settings.RetentionDays, MaxRows: settings.MaxRows, UpdatedAt: settings.UpdatedAt}
	if s.writer != nil {
		status.DroppedWrites = s.writer.Dropped()
		status.FailedWrites = s.writer.Failed()
	}
	return status, nil
}

func (s *Logs) UpdateSettings(ctx context.Context, settings model.LogSettings) (model.LogSettings, error) {
	if settings.Level == "" {
		settings.Level = "debug"
	}
	if settings.Level != "debug" && settings.Level != "info" && settings.Level != "warn" && settings.Level != "error" {
		return model.LogSettings{}, ErrLogSettingsInvalid
	}
	if settings.RetentionDays < 1 || settings.RetentionDays > 3650 || settings.MaxRows < 1000 || settings.MaxRows > 10000000 {
		return model.LogSettings{}, ErrLogSettingsInvalid
	}
	// Serialize saving and applying so concurrent updates cannot leave the writer
	// using an older setting than the one persisted in the database.
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if err := s.repository.SaveLogSettings(ctx, settings); err != nil {
		return model.LogSettings{}, fmt.Errorf("%w: %w", ErrLogSettingsUpdate, err)
	}
	if s.writer != nil {
		s.writer.SetEnabled(settings.Persistent)
		if levelWriter, ok := s.writer.(LogLevelWriteController); ok {
			levelWriter.SetMinLevel(settings.Level)
		}
	}
	s.logs.New("settings").Info("Runtime log retention settings updated")
	updated, err := s.repository.GetLogSettings(ctx)
	if err != nil {
		return model.LogSettings{}, fmt.Errorf("%w: %w", ErrLogSettingsUnavailable, err)
	}
	return updated, nil
}
