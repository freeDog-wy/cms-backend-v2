package model

import "time"

type User struct {
	ID int64 `json:"id"`

	Username     string  `json:"username"`
	Nickname     string  `json:"nickname"`
	Email        *string `json:"email"`
	PasswordHash string  `json:"-"`

	Status int16 `json:"status"`

	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func (User) TableName() string {
	return "users"
}

const (
	UserStatusEnabled  int16 = 1 // user enabled
	UserStatusDisabled int16 = 0 // user disabled
)
