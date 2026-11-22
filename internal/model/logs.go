package model

import "time"

type LogEntry struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Source    string    `json:"source"`
	Message   string    `json:"message"`
	RequestID string    `json:"requestId,omitempty"`
}

type LogSettings struct {
	Level         string    `json:"level"`
	Persistent    bool      `json:"persistent"`
	RetentionDays int       `json:"retentionDays"`
	MaxRows       int       `json:"maxRows"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type LogFilter struct {
	Level     string
	Component string
	RequestID string
	Query     string
	From      *time.Time
	To        *time.Time
	Limit     int
	Offset    int
}
