package application

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"easybot/internal/model"
	"easybot/internal/runtime"
)

type logRepositoryStub struct {
	items                              []model.LogEntry
	total                              int
	persisted                          map[string]bool
	filter                             model.LogFilter
	counted                            []string
	settings                           model.LogSettings
	listErr, countErr, getErr, saveErr error
	saves                              int
}

func (r *logRepositoryStub) ListLogs(_ context.Context, filter model.LogFilter) ([]model.LogEntry, int, error) {
	r.filter = filter
	return append([]model.LogEntry{}, r.items...), r.total, r.listErr
}
func (r *logRepositoryStub) CountLogIDs(_ context.Context, ids []string) (int, error) {
	r.counted = append([]string{}, ids...)
	count := 0
	for _, id := range ids {
		if r.persisted[id] {
			count++
		}
	}
	return count, r.countErr
}
func (r *logRepositoryStub) GetLogSettings(context.Context) (model.LogSettings, error) {
	return r.settings, r.getErr
}
func (r *logRepositoryStub) SaveLogSettings(_ context.Context, settings model.LogSettings) error {
	r.saves++
	if r.saveErr == nil {
		r.settings = settings
	}
	return r.saveErr
}

type logWriterStub struct {
	enabled bool
	updates int
}

func (*logWriterStub) Dropped() uint64           { return 2 }
func (*logWriterStub) Failed() uint64            { return 3 }
func (w *logWriterStub) SetEnabled(enabled bool) { w.enabled = enabled; w.updates++ }

func TestLogsMergeFirstPageAndRetainDatabaseOffsets(t *testing.T) {
	ctx := context.Background()
	memory := runtime.NewLogs(20, nil)
	memory.New("bridge").Info("pending first")
	memory.New("bridge").Info("pending second")
	recent := memory.Recent(20)
	// One runtime entry is persisted but outside the selected database page.
	repo := &logRepositoryStub{items: []model.LogEntry{recent[0]}, total: 2, persisted: map[string]bool{recent[1].ID: true}}
	service := NewLogs(repo, memory, nil)
	page, err := service.List(ctx, model.LogFilter{Limit: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != recent[0].ID {
		t.Fatalf("deduplication/total: %+v %v", page, err)
	}
	if !reflect.DeepEqual(repo.counted, []string{recent[1].ID}) {
		t.Fatalf("counted IDs: %v", repo.counted)
	}
	repo.persisted = nil
	page, err = service.List(ctx, model.LogFilter{Limit: 2})
	if err != nil || page.Total != 3 || len(page.Items) != 2 {
		t.Fatalf("pending total: %+v %v", page, err)
	}
	repo.counted = nil
	page, err = service.List(ctx, model.LogFilter{Limit: 2, Offset: 2})
	if err != nil || page.Total != 2 || page.Offset != 2 || len(page.Items) != 1 || repo.counted != nil {
		t.Fatalf("database pagination contract: %+v %v", page, err)
	}
}

func TestLogsMemoryFiltersAndDefaults(t *testing.T) {
	memory := runtime.NewLogs(20, nil)
	memory.New("bridge").With("request_id", "wanted").Error("Connection REFUSED")
	memory.New("adapter").With("request_id", "other").Warn("unrelated")
	entries := memory.Recent(20)
	target := entries[1]
	repo := &logRepositoryStub{}
	service := NewLogs(repo, memory, nil)
	from, to := target.Time, target.Time.Add(time.Nanosecond)
	filter := model.LogFilter{Level: "error", Component: "bridge", RequestID: "wanted", Query: " refused ", From: &from, To: &to}
	page, err := service.List(context.Background(), filter)
	if err != nil || page.Limit != 100 || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != target.ID || repo.filter.Query != "refused" {
		t.Fatalf("filter/defaults: %+v %v filter=%+v", page, err, repo.filter)
	}
	for _, change := range []func(*model.LogFilter){
		func(f *model.LogFilter) { f.Level = "warn" },
		func(f *model.LogFilter) { f.Component = "adapter" },
		func(f *model.LogFilter) { f.RequestID = "other" },
		func(f *model.LogFilter) { f.Query = "absent" },
		func(f *model.LogFilter) { after := target.Time.Add(time.Nanosecond); f.From = &after },
		func(f *model.LogFilter) { f.To = &target.Time },
	} {
		changed := filter
		change(&changed)
		page, err := service.List(context.Background(), changed)
		if err != nil || len(page.Items) != 0 || page.Total != 0 {
			t.Fatalf("nonmatching filter: %+v %v", page, err)
		}
	}
}

func TestLogsStableSortAndQueryFailures(t *testing.T) {
	memory := runtime.NewLogs(10, nil)
	at := time.Now()
	repo := &logRepositoryStub{items: []model.LogEntry{{ID: "a", Time: at}, {ID: "b", Time: at}}, total: 2}
	service := NewLogs(repo, memory, nil)
	page, err := service.List(context.Background(), model.LogFilter{})
	if err != nil || page.Items[0].ID != "b" {
		t.Fatalf("tie order: %+v %v", page, err)
	}
	for _, test := range []struct {
		filter model.LogFilter
		want   error
	}{
		{model.LogFilter{Limit: -1}, ErrLogLimit},
		{model.LogFilter{Limit: 501}, ErrLogLimit},
		{model.LogFilter{Offset: -1}, ErrLogOffset},
	} {
		if _, err := service.List(context.Background(), test.filter); !errors.Is(err, test.want) {
			t.Fatalf("pagination validation: %v", err)
		}
	}
	failure := errors.New("query failed")
	repo.listErr = failure
	if _, err := service.List(context.Background(), model.LogFilter{}); !errors.Is(err, failure) {
		t.Fatalf("list error: %v", err)
	}
	repo.listErr, repo.countErr = nil, failure
	if _, err := service.List(context.Background(), model.LogFilter{}); !errors.Is(err, failure) {
		t.Fatalf("count error: %v", err)
	}
}

func TestLogSettingsValidationAndCoordination(t *testing.T) {
	ctx := context.Background()
	memory := runtime.NewLogs(20, nil)
	repo := &logRepositoryStub{}
	writer := &logWriterStub{enabled: true}
	service := NewLogs(repo, memory, writer)
	valid := model.LogSettings{Level: "debug", Persistent: false, RetentionDays: 30, MaxRows: 1000}
	for _, settings := range []model.LogSettings{
		{Level: "debug", RetentionDays: 0, MaxRows: 1000}, {Level: "debug", RetentionDays: 3651, MaxRows: 1000},
		{Level: "debug", RetentionDays: 1, MaxRows: 999}, {Level: "debug", RetentionDays: 1, MaxRows: 10000001},
	} {
		if _, err := service.UpdateSettings(ctx, settings); !errors.Is(err, ErrLogSettingsInvalid) {
			t.Fatalf("invalid settings accepted: %v", err)
		}
	}
	if repo.saves != 0 || writer.updates != 0 || len(memory.Recent(20)) != 0 {
		t.Fatal("invalid settings produced side effects")
	}
	failure := errors.New("db unavailable")
	repo.saveErr = failure
	if _, err := service.UpdateSettings(ctx, valid); !errors.Is(err, ErrLogSettingsUpdate) || !errors.Is(err, failure) {
		t.Fatalf("save error: %v", err)
	}
	if writer.updates != 0 || len(memory.Recent(20)) != 0 {
		t.Fatal("failed save changed runtime")
	}
	repo.saveErr = nil
	updated, err := service.UpdateSettings(ctx, valid)
	if err != nil || updated != valid || writer.enabled || writer.updates != 1 || len(memory.Recent(20)) != 1 {
		t.Fatalf("update: %+v %v writer=%+v", updated, err, writer)
	}
	status, err := service.Status(ctx)
	if err != nil || status.Persistent || status.RetentionDays != 30 || status.DroppedWrites != 2 || status.FailedWrites != 3 {
		t.Fatalf("status: %+v %v", status, err)
	}
	repo.getErr = failure
	if _, err := service.UpdateSettings(ctx, valid); !errors.Is(err, ErrLogSettingsUnavailable) {
		t.Fatalf("read-back error: %v", err)
	}
	if writer.updates != 2 {
		t.Fatal("runtime must apply saved settings even if read-back fails")
	}
	if _, err := service.Status(ctx); !errors.Is(err, ErrLogSettingsUnavailable) {
		t.Fatalf("status error: %v", err)
	}
	repo.getErr = nil
	noWriter := NewLogs(repo, memory, nil)
	if status, err := noWriter.Status(ctx); err != nil || status.DroppedWrites != 0 || status.FailedWrites != 0 {
		t.Fatalf("no writer status: %+v %v", status, err)
	}
	for _, settings := range []model.LogSettings{
		{Level: "debug", RetentionDays: 1, MaxRows: 1000}, {Level: "debug", RetentionDays: 3650, MaxRows: 10000000},
	} {
		if _, err := noWriter.UpdateSettings(ctx, settings); err != nil {
			t.Fatalf("boundary settings rejected: %v", err)
		}
	}
}

func TestLogSettingsConcurrentUpdatesStayConsistent(t *testing.T) {
	repo := &logRepositoryStub{}
	writer := &logWriterStub{}
	service := NewLogs(repo, runtime.NewLogs(20, nil), writer)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(persistent bool) {
			defer wg.Done()
			_, err := service.UpdateSettings(context.Background(), model.LogSettings{Level: "debug", Persistent: persistent, RetentionDays: 30, MaxRows: 1000})
			if err != nil {
				t.Error(err)
			}
		}(i%2 == 0)
	}
	wg.Wait()
	if writer.enabled != repo.settings.Persistent || writer.updates != 30 {
		t.Fatalf("inconsistent runtime and persistence: %+v %+v", repo.settings, writer)
	}
}
