package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/event"
	"github.com/freeDog-wy/cms-backend-v2/internal/infra"
	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/pkg"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo/generated"
	"gorm.io/gorm"
)

type CommentReviewTask struct {
	db     *gorm.DB
	sender pkg.EmailSender
	to     string
}

func NewCommentReviewTask(db *gorm.DB, sender pkg.EmailSender, adminEmail string) (*CommentReviewTask, error) {
	address, err := mail.ParseAddress(adminEmail)
	if err != nil || strings.ContainsAny(adminEmail, "\r\n") ||
		strings.ContainsFunc(address.Address, func(r rune) bool { return r > 127 }) {
		return nil, errors.New("invalid comment review recipient")
	}
	return &CommentReviewTask{
		db: db, sender: sender, to: address.Address,
	}, nil
}

func (t *CommentReviewTask) Handle(ctx context.Context, payload json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var input event.CommentSubmittedPayload
	if err := json.Unmarshal(payload, &input); err != nil || input.CommentID <= 0 {
		return fmt.Errorf("%w: expected positive comment_id string", infra.ErrInvalidTaskPayload)
	}
	comment, err := repo.One(generated.CommentRepo[model.Comment](t.db).GetByID(ctx, input.CommentID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load comment for review notification: %w", err)
	}
	if comment.DeletedAt != nil || comment.Status != model.CommentStatusPending || comment.UserID != nil {
		return nil
	}
	if comment.GuestName == nil {
		return fmt.Errorf("%w: guest comment has no author", infra.ErrInvalidTaskPayload)
	}
	parent := ""
	if comment.ParentID != nil {
		parent = fmt.Sprintf("父评论编号：%d\n", *comment.ParentID)
	}
	body := fmt.Sprintf("有新的访客评论等待审核。\n\n评论编号：%d\n文章标识：%s\n%s访客昵称：%s\n提交时间：%s\n\n评论内容：\n%s\n\n请登录管理后台审核。\n此邮件为提交提醒，评论当前状态以后台为准。",
		comment.ID, comment.ArticleKey, parent, *comment.GuestName, comment.CreatedAt.Format(time.RFC3339), comment.Content)
	return t.sender.Send(ctx, pkg.EmailMessage{
		// 文章 UUID 与评论 ID 共同标识通知，重试时保持一致。
		MessageID: fmt.Sprintf("comment-review-%s-%d@cms.local", comment.ArticleKey, comment.ID),
		To:        t.to, Subject: "[CMS] 有新的访客评论待审核", Text: body,
	})
}
