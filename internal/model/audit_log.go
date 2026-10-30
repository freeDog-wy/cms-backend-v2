package model

import (
	"time"

	"gorm.io/datatypes"
)

// AuditLog 管理操作审计日志。
type AuditLog struct {
	ID int64 `json:"id,string"`

	// 执行操作的用户。
	ActorUserID int64 `json:"actor_user_id,string"`

	// 资源类型，例如 comment、user、role。
	ResourceType string `json:"resource_type"`

	// 资源主键的字符串表示，兼容整数、UUID 等类型。
	ResourceID string `json:"resource_id"`

	// 操作标识，例如 comment:moderate、user:disable。
	Action string `json:"action"`

	// 操作前后的关键字段，可以为空。
	BeforeData datatypes.JSON `json:"before_data"`
	AfterData  datatypes.JSON `json:"after_data"`

	Reason string `json:"reason"`

	CreatedAt time.Time `json:"created_at"`
}

func (AuditLog) TableName() string {
	return "audit_logs"
}

const (
	AuditResourceComment = "comment"
	AuditResourceUser    = "user"
	AuditResourceRole    = "role"
)

const (
	AuditActionCommentModerate = "comment:moderate"
	AuditActionCommentDelete   = "comment:delete"
	AuditActionCommentCreate   = "comment:create"

	AuditActionUserDisable = "user:disable"
	AuditActionUserEnable  = "user:enable"
)
