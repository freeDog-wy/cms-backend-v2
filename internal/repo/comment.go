package repo

import (
	"context"

	"github.com/freeDog-wy/cms-backend-v2/internal/model"
)

//go:generate go run gorm.io/cli/gorm gen -i $GOFILE -o ./generated

type CommentRepo[T any] interface {
	// 在当前事务中串行处理相同的匿名提交标识，事务结束自动释放。
	//
	// SELECT pg_advisory_xact_lock(hashtextextended(CAST(@requestID AS text), 0))
	LockRequest(ctx context.Context, requestID string) error

	// 查询评论，包括已软删除的记录。
	//
	// SELECT *
	// FROM @@table
	// WHERE id = @id
	GetByID(ctx context.Context, id int64) ([]T, error)

	// 在事务中锁定评论。
	// 用于检查回复的父评论，以及读取审核前的状态。
	//
	// SELECT *
	// FROM @@table
	// WHERE id = @id
	// FOR UPDATE
	GetByIDForUpdate(ctx context.Context, id int64) ([]T, error)

	// 根据提交标识查询，包括已软删除的记录。
	//
	// SELECT *
	// FROM @@table
	// WHERE request_id = CAST(@requestID AS uuid)
	GetByRequestID(
		ctx context.Context,
		requestID string,
	) ([]T, error)

	// 提交匿名评论，状态固定为待审核。
	// 提交标识重复时不插入，返回空切片。
	//
	// INSERT INTO @@table (
	//     article_key, parent_id, guest_name, content, request_id
	// )
	// VALUES (
	//     CAST(@comment.ArticleKey AS uuid),
	//     @comment.ParentID,
	//     @comment.GuestName,
	//     @comment.Content,
	//     CAST(@comment.RequestID AS uuid)
	// )
	// ON CONFLICT (request_id) DO NOTHING
	// RETURNING *
	InsertGuest(
		ctx context.Context,
		comment model.Comment,
	) ([]T, error)

	// 管理员发布评论，直接通过审核。
	// 作者及审核人都由服务层提供的当前用户决定。
	//
	// INSERT INTO @@table (
	//     article_key, parent_id, user_id, content, request_id,
	//     status, reviewed_by, reviewed_at
	// )
	// VALUES (
	//     CAST(@comment.ArticleKey AS uuid),
	//     @comment.ParentID,
	//     @actorUserID,
	//     @comment.Content,
	//     CAST(@comment.RequestID AS uuid),
	//     'approved',
	//     @actorUserID,
	//     CURRENT_TIMESTAMP
	// )
	// RETURNING *
	InsertStaff(
		ctx context.Context,
		comment model.Comment,
		actorUserID int64,
	) ([]T, error)

	// 后台查询，排除已删除评论。
	// status 为空表示全部状态，articleKey 为 nil 表示全部文章。
	// beforeID 为 0 表示查询最新一页。
	//
	// SELECT *
	// FROM @@table
	// WHERE deleted_at IS NULL
	//   AND (CAST(@status AS text) = '' OR status = @status)
	//   AND (
	//       CAST(@articleKey AS uuid) IS NULL
	//       OR article_key = CAST(@articleKey AS uuid)
	//   )
	//   AND (CAST(@beforeID AS bigint) = 0 OR id < @beforeID)
	// ORDER BY id DESC
	// LIMIT @limit
	ListForAdmin(
		ctx context.Context,
		status model.CommentStatus,
		articleKey *string,
		beforeID int64,
		limit int,
	) ([]T, error)

	// 审核评论，同时校验版本并递增版本号。
	// 没有匹配记录时返回空切片。
	//
	// UPDATE @@table
	// SET status = @status,
	//     reviewed_by = @actorUserID,
	//     reviewed_at = CURRENT_TIMESTAMP,
	//     moderation_reason = @reason,
	//     version = version + 1,
	//     updated_at = CURRENT_TIMESTAMP
	// WHERE id = @id
	//   AND version = @version
	//   AND deleted_at IS NULL
	// RETURNING *
	Moderate(
		ctx context.Context,
		id int64,
		version int64,
		status model.CommentStatus,
		actorUserID int64,
		reason string,
	) ([]T, error)

	// 软删除评论，不改变审核状态。
	//
	// UPDATE @@table
	// SET deleted_at = CURRENT_TIMESTAMP,
	//     updated_at = CURRENT_TIMESTAMP,
	//     version = version + 1
	// WHERE id = @id
	//   AND version = @version
	//   AND deleted_at IS NULL
	// RETURNING *
	SoftDelete(
		ctx context.Context,
		id int64,
		version int64,
	) ([]T, error)

	// 公开的顶层评论列表。
	// 已删除的顶层评论保留占位，但隐藏作者和正文。
	//
	// SELECT
	//     c.id,
	//     c.parent_id,
	//     CASE
	//         WHEN c.deleted_at IS NOT NULL THEN ''
	//         WHEN c.user_id IS NULL THEN c.guest_name
	//         ELSE '管理员'
	//     END AS author_name,
	//     (
	//         c.user_id IS NOT NULL
	//         AND c.deleted_at IS NULL
	//     ) AS is_staff,
	//     CASE
	//         WHEN c.deleted_at IS NOT NULL THEN ''
	//         ELSE c.content
	//     END AS content,
	//     (c.deleted_at IS NOT NULL) AS is_deleted,
	//     (
	//         SELECT count(*)
	//         FROM @@table AS child
	//         WHERE child.parent_id = c.id
	//           AND child.status = 'approved'
	//           AND child.deleted_at IS NULL
	//     ) AS reply_count,
	//     c.created_at
	// FROM @@table AS c
	// WHERE c.article_key = CAST(@articleKey AS uuid)
	//   AND c.parent_id IS NULL
	//   AND c.status = 'approved'
	//   AND c.id > @afterID
	// ORDER BY c.id
	// LIMIT @limit
	ListPublicRoots(
		ctx context.Context,
		articleKey string,
		afterID int64,
		limit int,
	) ([]model.PublicComment, error)

	// 公开的回复列表。
	// 父评论必须为已通过审核的顶层评论。
	// 父评论软删除后仍允许查看已有回复。
	//
	// SELECT
	//     c.id,
	//     c.parent_id,
	//     CASE
	//         WHEN c.user_id IS NULL THEN c.guest_name
	//         ELSE '管理员'
	//     END AS author_name,
	//     (c.user_id IS NOT NULL) AS is_staff,
	//     c.content,
	//     FALSE AS is_deleted,
	//     0::bigint AS reply_count,
	//     c.created_at
	// FROM @@table AS c
	// WHERE c.article_key = CAST(@articleKey AS uuid)
	//   AND c.parent_id = @parentID
	//   AND c.status = 'approved'
	//   AND c.deleted_at IS NULL
	//   AND c.id > @afterID
	//   AND EXISTS (
	//       SELECT 1
	//       FROM @@table AS parent
	//       WHERE parent.id = c.parent_id
	//         AND parent.article_key = c.article_key
	//         AND parent.parent_id IS NULL
	//         AND parent.status = 'approved'
	//   )
	// ORDER BY c.id
	// LIMIT @limit
	ListPublicReplies(
		ctx context.Context,
		articleKey string,
		parentID int64,
		afterID int64,
		limit int,
	) ([]model.PublicComment, error)
}
