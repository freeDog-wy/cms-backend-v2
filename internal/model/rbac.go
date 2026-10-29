package model

import "time"

// Role 角色。
type Role struct {
	ID int64 `json:"id"`

	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`

	Status int16 `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Role) TableName() string {
	return "roles"
}

const (
	RoleStatusEnabled  int16 = 1 // 启用
	RoleStatusDisabled int16 = 0 // 禁用
)

// Permission 业务权限。
type Permission struct {
	ID int64 `json:"id"`

	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (Permission) TableName() string {
	return "permissions"
}

// UserRole 用户与角色的关联。
type UserRole struct {
	UserID int64 `json:"user_id"`
	RoleID int64 `json:"role_id"`

	CreatedAt time.Time `json:"created_at"`
}

func (UserRole) TableName() string {
	return "user_roles"
}

// RolePermission 角色与权限的关联。
type RolePermission struct {
	RoleID       int64 `json:"role_id"`
	PermissionID int64 `json:"permission_id"`

	CreatedAt time.Time `json:"created_at"`
}

func (RolePermission) TableName() string {
	return "role_permissions"
}
