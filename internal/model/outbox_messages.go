package model

import (
	"time"

	"gorm.io/datatypes"
)

type OutboxStatus string

const (
	OutboxStatusPending    OutboxStatus = "pending"
	OutboxStatusProcessing OutboxStatus = "processing"
	OutboxStatusSucceeded  OutboxStatus = "succeeded"
	OutboxStatusFailed     OutboxStatus = "failed"
)

type OutboxMessage struct {
	ID int64 `json:"id"`

	EventType string         `json:"event_type"`
	Payload   datatypes.JSON `json:"payload"`

	Status OutboxStatus `json:"status"`

	Attempts    int32 `json:"attempts"`
	MaxAttempts int32 `json:"max_attempts"`

	// 首次执行或下次重试允许被领取的时间。
	AvailableAt time.Time `json:"available_at"`

	// 仅 processing 状态非空。
	LeaseToken  *string    `json:"lease_token,omitempty"`
	LockedUntil *time.Time `json:"locked_until,omitempty"`

	LastError  *string    `json:"last_error,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (OutboxMessage) TableName() string {
	return "outbox_messages"
}
