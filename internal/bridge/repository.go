package bridge

import (
	"context"
	"easybot/internal/model"
)

// Repository is the configuration/query boundary of the bridge runtime.
// SQL is implemented by store; transport does not own database connections.
type Repository interface {
	Get(context.Context, string, string) (model.Record, error)
	List(context.Context, string) ([]model.Record, error)
	IsPlayerBound(context.Context, string) (bool, error)
}
