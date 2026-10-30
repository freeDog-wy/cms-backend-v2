package repo

import (
	"context"

	"github.com/freeDog-wy/cms-backend-v2/internal/model"
)

//go:generate go run gorm.io/cli/gorm gen -i $GOFILE -o ./generated

type AuditLogRepo[T any] interface {
	// 插入审计日志，id 和 created_at 使用数据库默认值。
	//
	// INSERT INTO @@table (
	//     actor_user_id,
	//     resource_type,
	//     resource_id,
	//     action,
	//     before_data,
	//     after_data,
	//     reason
	// )
	// VALUES (
	//     @auditLog.ActorUserID,
	//     @auditLog.ResourceType,
	//     @auditLog.ResourceID,
	//     @auditLog.Action,
	//     CAST(@auditLog.BeforeData AS jsonb),
	//     CAST(@auditLog.AfterData AS jsonb),
	//     @auditLog.Reason
	// )
	Insert(ctx context.Context, auditLog model.AuditLog) error

	// 查询资源的操作历史，按 ID 倒序分页。
	// beforeID 为 0 时查询最新记录。
	//
	// SELECT *
	// FROM @@table
	// WHERE resource_type = @resourceType
	//   AND resource_id = @resourceID
	//   AND (
	//       CAST(@beforeID AS bigint) = 0
	//       OR id < @beforeID
	//   )
	// ORDER BY id DESC
	// LIMIT @limit
	ListByResource(
		ctx context.Context,
		resourceType string,
		resourceID string,
		beforeID int64,
		limit int,
	) ([]T, error)

	// 查询用户执行的操作，按 ID 倒序分页。
	// beforeID 为 0 时查询最新记录。
	//
	// SELECT *
	// FROM @@table
	// WHERE actor_user_id = @actorUserID
	//   AND (
	//       CAST(@beforeID AS bigint) = 0
	//       OR id < @beforeID
	//   )
	// ORDER BY id DESC
	// LIMIT @limit
	ListByActor(
		ctx context.Context,
		actorUserID int64,
		beforeID int64,
		limit int,
	) ([]T, error)
}
