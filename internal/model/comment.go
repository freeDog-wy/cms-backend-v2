package model

import "time"

// Comment 文章评论。
type Comment struct {
	ID int64 `json:"id,string"`

	// 静态站文章的 commentId，对应 PostgreSQL UUID。
	ArticleKey string `json:"article_key"`

	// NULL 表示顶层评论，否则指向所属的顶层评论。
	ParentID *int64 `json:"parent_id,string"`

	// 匿名作者填写 GuestName，管理员作者填写 UserID。
	UserID    *int64  `json:"user_id,string"`
	GuestName *string `json:"guest_name"`

	Content string        `json:"content"`
	Status  CommentStatus `json:"status"`

	// 审核、删除等修改操作成功后递增。
	Version int64 `json:"version"`

	// 提交幂等标识，不直接向客户端暴露。
	RequestID *string `json:"-"`

	// 最近一次审核信息。
	ReviewedBy       *int64     `json:"reviewed_by,string"`
	ReviewedAt       *time.Time `json:"reviewed_at"`
	ModerationReason string     `json:"moderation_reason"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at"`
}

func (Comment) TableName() string {
	return "comments"
}

// CommentStatus 评论审核状态。
type CommentStatus string

const (
	CommentStatusPending  CommentStatus = "pending"  // 待审核
	CommentStatusApproved CommentStatus = "approved" // 已通过
	CommentStatusRejected CommentStatus = "rejected" // 已拒绝
)

// PublicComment 公开展示的评论。非数据库表映射对象
type PublicComment struct {
	ID         int64     `json:"id,string"`
	ParentID   *int64    `json:"parent_id,string"`
	AuthorName string    `json:"author_name"`
	IsStaff    bool      `json:"is_staff"`
	Content    string    `json:"content"`
	IsDeleted  bool      `json:"is_deleted"`
	ReplyCount int64     `json:"reply_count"`
	CreatedAt  time.Time `json:"created_at"`
}
