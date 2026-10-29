package model

import "time"

// Session 用户登录会话。
type Session struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"user_id"`

	// 存储 SHA-256 摘要，对应 PostgreSQL 的 BYTEA。
	TokenHash []byte `json:"-"`

	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (Session) TableName() string {
	return "sessions"
}