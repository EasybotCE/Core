package application

import (
	"context"
	"easybot/internal/model"
)

// Record 沿用现有记录的数据形状；本轮只隔离用例，不改变持久化模型和 HTTP 契约。
type Record = model.Record

type RecordRepository interface {
	Get(context.Context, string, string) (Record, error)
	List(context.Context, string) ([]Record, error)
	Save(context.Context, Record) (Record, error)
	Delete(context.Context, string, string) error
}

// ValidationError 表示用例输入不满足要求，不携带 HTTP 状态。
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func (e *ValidationError) Is(target error) bool {
	other, ok := target.(*ValidationError)
	return ok && e.Message == other.Message
}

type Records struct{ repository RecordRepository }

func NewRecords(repository RecordRepository) *Records { return &Records{repository: repository} }

// 群组只能从平台获取；保留数据库 kind 以兼容已有数据。
func recordKindAvailable(kind string) bool { return kind != "groups" && model.Kinds[kind] }

func (s *Records) List(ctx context.Context, kind string) ([]Record, error) {
	if !recordKindAvailable(kind) {
		return nil, &ValidationError{Message: "unknown record kind"}
	}
	items, err := s.repository.List(ctx, kind)
	if err != nil {
		return nil, err
	}
	if kind == "adapters" {
		for i := range items {
			items[i] = publicAdapterRecord(items[i])
		}
	}
	return items, nil
}

func (s *Records) Create(ctx context.Context, item Record) (Record, error) {
	if !recordKindAvailable(item.Kind) {
		return Record{}, unavailableRecordKind()
	}
	if item.Kind == "servers" {
		detail, err := ensureServerToken(item.Detail)
		if err != nil {
			return Record{}, &ValidationError{Message: "server config must be valid JSON"}
		}
		item.Detail = detail
	}
	item, err := s.repository.Save(ctx, item)
	if err != nil {
		return Record{}, err
	}
	return publicRecord(item), nil
}

func (s *Records) Update(ctx context.Context, item Record) (Record, error) {
	if !recordKindAvailable(item.Kind) {
		return Record{}, unavailableRecordKind()
	}
	if item.Kind == "servers" && serverToken(item.Detail) == "" {
		if existing, err := s.repository.Get(ctx, "servers", item.ID); err == nil {
			if token := serverToken(existing.Detail); token != "" {
				if detail, err := setServerToken(item.Detail, token); err == nil {
					item.Detail = detail
				}
			}
		}
	}
	item, err := s.repository.Save(ctx, item)
	if err != nil {
		return Record{}, err
	}
	return publicRecord(item), nil
}

func (s *Records) Delete(ctx context.Context, kind, id string) error {
	if !recordKindAvailable(kind) {
		return unavailableRecordKind()
	}
	return s.repository.Delete(ctx, kind, id)
}

func unavailableRecordKind() error {
	return &ValidationError{Message: "群组列表由平台适配器 API 提供，不支持手动配置"}
}
func publicRecord(item Record) Record {
	if item.Kind == "adapters" {
		return publicAdapterRecord(item)
	}
	return item
}
