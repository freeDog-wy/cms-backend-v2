package repo

import (
	"context"

	"github.com/freeDog-wy/cms-backend-v2/internal/model"
)

//go:generate go run gorm.io/cli/gorm gen -i $GOFILE -o ./generated

type SessionRepo[T any] interface {
	// 创建会话，created_at 使用数据库默认值。
	//
	// INSERT INTO @@table (
	//     user_id, token_hash, expires_at
	// )
	// VALUES (
	//     @session.UserID, @session.TokenHash, @session.ExpiresAt
	// )
	// RETURNING *
	InsertSession(
		ctx context.Context,
		session model.Session,
	) (*T, error)

	// 查询有效会话，同时确认用户处于启用状态。
	//
	// SELECT s.*
	// FROM @@table AS s
	// WHERE s.token_hash = @tokenHash
	//   AND s.expires_at > CURRENT_TIMESTAMP
	//   AND EXISTS (
	//       SELECT 1
	//       FROM users AS u
	//       WHERE u.id = s.user_id
	//         AND u.status = 1
	//   )
	GetValidByTokenHash(
		ctx context.Context,
		tokenHash []byte,
	) (*T, error)

	// 注销当前会话。
	//
	// DELETE FROM @@table
	// WHERE token_hash = @tokenHash
	DeleteByTokenHash(
		ctx context.Context,
		tokenHash []byte,
	) error

	// 注销指定用户的所有会话。
	//
	// DELETE FROM @@table
	// WHERE user_id = @userID
	DeleteByUserID(
		ctx context.Context,
		userID int64,
	) error

	// 分批清理过期会话，返回本批删除数量。
	//
	// WITH expired AS (
	//     SELECT id
	//     FROM @@table
	//     WHERE expires_at <= CURRENT_TIMESTAMP
	//     ORDER BY expires_at, id
	//     LIMIT @batchSize
	//     FOR UPDATE SKIP LOCKED
	// ), deleted AS (
	//     DELETE FROM @@table AS s
	//     USING expired AS e
	//     WHERE s.id = e.id
	//     RETURNING s.id
	// )
	// SELECT count(*) FROM deleted
	DeleteExpiredBatch(ctx context.Context, batchSize int) (int64, error)
}
