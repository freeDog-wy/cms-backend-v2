package srv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo/generated"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// CommentSrv 处理评论规则和事务；访问权限由调用入口的中间件检查。
type CommentSrv struct {
	db *gorm.DB

	// 保存最近一次有效文章清单，刷新失败不覆盖已有快照。
	manifestURL         string
	manifestClient      *http.Client
	manifestRefreshMu   sync.Mutex
	manifestMu          sync.RWMutex
	manifestArticles    map[string]bool
	manifestLastSuccess time.Time
}

func NewCommentSrv(db *gorm.DB, baseURL string) *CommentSrv {
	return &CommentSrv{
		db:          db,
		manifestURL: strings.TrimRight(baseURL, "/") + "/comment-manifest.json",
		manifestClient: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type CommentManifestStatus struct {
	Loaded       bool       `json:"loaded"`
	ArticleCount int        `json:"article_count"`
	LastSuccess  *time.Time `json:"last_success"`
}

func (s *CommentSrv) RefreshManifest(ctx context.Context) error {
	s.manifestRefreshMu.Lock()
	defer s.manifestRefreshMu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.manifestURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.manifestClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("comment manifest HTTP status %d", resp.StatusCode)
	}
	const maxSize = 2 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxSize {
		return fmt.Errorf("comment manifest too large")
	}
	var document struct {
		SchemaVersion int    `json:"schemaVersion"`
		Version       string `json:"version"`
		Articles      []struct {
			ID              string `json:"id"`
			Title           string `json:"title"`
			Path            string `json:"path"`
			CommentsEnabled *bool  `json:"commentsEnabled"`
		} `json:"articles"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	if document.SchemaVersion != 1 || document.Version == "" || len(document.Version) > 128 || document.Articles == nil || len(document.Articles) > 20000 {
		return fmt.Errorf("invalid comment manifest")
	}
	articles := make(map[string]bool, len(document.Articles))
	paths := make(map[string]bool, len(document.Articles))
	for _, article := range document.Articles {
		id, err := uuid.Parse(article.ID)
		if err != nil || id == uuid.Nil || article.CommentsEnabled == nil || article.Title == "" || len(article.Path) == 0 || article.Path[0] != '/' {
			return fmt.Errorf("invalid manifest article")
		}
		key := id.String()
		if _, exists := articles[key]; exists || paths[article.Path] {
			return fmt.Errorf("duplicate manifest article")
		}
		articles[key] = *article.CommentsEnabled
		paths[article.Path] = true
	}
	s.manifestMu.Lock()
	s.manifestArticles, s.manifestLastSuccess = articles, time.Now()
	s.manifestMu.Unlock()
	return nil
}

func (s *CommentSrv) requireArticleReadable(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.manifestMu.RLock()
	defer s.manifestMu.RUnlock()
	if s.manifestArticles == nil {
		return false, errorx.ErrCommentUnavailable
	}
	enabled, exists := s.manifestArticles[key]
	if !exists {
		return false, errorx.ErrNotFound
	}
	return enabled, nil
}

func (s *CommentSrv) requireArticleWritable(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.manifestMu.RLock()
	defer s.manifestMu.RUnlock()
	if s.manifestArticles == nil || time.Since(s.manifestLastSuccess) > time.Hour {
		return errorx.ErrCommentUnavailable
	}
	enabled, exists := s.manifestArticles[key]
	if !exists {
		return errorx.ErrNotFound
	}
	if !enabled {
		return errorx.ErrCommentsClosed
	}
	return nil
}

func (s *CommentSrv) ManifestStatus() CommentManifestStatus {
	s.manifestMu.RLock()
	defer s.manifestMu.RUnlock()
	status := CommentManifestStatus{Loaded: s.manifestArticles != nil, ArticleCount: len(s.manifestArticles)}
	if !s.manifestLastSuccess.IsZero() {
		t := s.manifestLastSuccess
		status.LastSuccess = &t
	}
	return status
}

type CreateGuestCommentCmd struct {
	ArticleKey string
	ParentID   *int64
	GuestName  string
	Content    string
	RequestID  string
}
type CreateStaffCommentCmd struct {
	ArticleKey string
	ParentID   *int64
	Content    string
}
type CommentSubmission struct {
	ID int64 `json:"id,string"`
}

func normalizeCommentText(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
}

func validCommentText(s string, max int, multiline bool) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > max {
		return false
	}
	for _, r := range s {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func commentArticleKey(key string) (string, error) {
	id, err := uuid.Parse(key)
	if err != nil || id == uuid.Nil {
		return "", errorx.ErrInvalidArgument
	}
	return id.String(), nil
}

func sameCommentParent(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func matchGuestSubmission(existing *model.Comment, input model.Comment) (*model.Comment, error) {
	if existing.UserID != nil ||
		existing.GuestName == nil ||
		existing.ArticleKey != input.ArticleKey ||
		*existing.GuestName != *input.GuestName ||
		existing.Content != input.Content ||
		!sameCommentParent(existing.ParentID, input.ParentID) {
		return nil, errorx.ErrConflict
	}
	return existing, nil
}

func (s *CommentSrv) CreateGuest(ctx context.Context, cmd CreateGuestCommentCmd) (*CommentSubmission, error) {
	key, err := commentArticleKey(cmd.ArticleKey)
	if err != nil {
		return nil, err
	}
	requestID, err := commentArticleKey(cmd.RequestID)
	if err != nil {
		return nil, err
	}
	name, content := strings.TrimSpace(cmd.GuestName), normalizeCommentText(cmd.Content)
	if !validCommentText(name, 40, false) || !validCommentText(content, 2000, true) || (cmd.ParentID != nil && *cmd.ParentID <= 0) {
		return nil, errorx.ErrInvalidArgument
	}
	input := model.Comment{
		ArticleKey: key,
		ParentID:   cmd.ParentID,
		GuestName:  &name,
		Content:    content,
		RequestID:  &requestID,
	}
	var result *model.Comment
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r := generated.CommentRepo[model.Comment](tx)
		// 同一提交标识串行处理，先识别重试，再检查可能已改变的父评论状态。
		if err := r.LockRequest(ctx, requestID); err != nil {
			return err
		}
		existing, err := repo.One(r.GetByRequestID(ctx, requestID))
		if err == nil {
			result, err = matchGuestSubmission(existing, input)
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err = s.requireArticleWritable(ctx, key); err != nil {
			return err
		}
		if err = checkCommentParent(ctx, tx, key, cmd.ParentID); err != nil {
			return err
		}
		rows, err := r.InsertGuest(ctx, input)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			result = &rows[0]
			return nil
		}
		existing, err = repo.One(r.GetByRequestID(ctx, requestID))
		if err != nil {
			return err
		}
		result, err = matchGuestSubmission(existing, input)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &CommentSubmission{ID: result.ID}, nil
}

func checkCommentParent(ctx context.Context, tx *gorm.DB, key string, parentID *int64) error {
	if parentID == nil {
		return nil
	}
	parent, err := repo.One(generated.CommentRepo[model.Comment](tx).GetByIDForUpdate(ctx, *parentID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errorx.ErrNotFound
	}
	if err != nil {
		return err
	}
	if parent.ArticleKey != key {
		return errorx.ErrNotFound
	}
	if parent.ParentID != nil || parent.Status != model.CommentStatusApproved || parent.DeletedAt != nil {
		return errorx.ErrConflict
	}
	return nil
}

func (s *CommentSrv) CreateStaff(ctx context.Context, actorID int64, cmd CreateStaffCommentCmd) (*model.Comment, error) {
	key, err := commentArticleKey(cmd.ArticleKey)
	if err != nil {
		return nil, err
	}
	content := normalizeCommentText(cmd.Content)
	if actorID <= 0 || !validCommentText(content, 2000, true) || (cmd.ParentID != nil && *cmd.ParentID <= 0) {
		return nil, errorx.ErrInvalidArgument
	}
	var result *model.Comment
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.requireArticleWritable(ctx, key); err != nil {
			return err
		}
		if err := checkCommentParent(ctx, tx, key, cmd.ParentID); err != nil {
			return err
		}
		var err error
		result, err = repo.One(generated.CommentRepo[model.Comment](tx).InsertStaff(
			ctx,
			model.Comment{
				ArticleKey: key,
				ParentID:   cmd.ParentID,
				Content:    content,
			},
			actorID,
		))
		if err != nil {
			return err
		}
		return insertCommentAudit(ctx, tx, actorID, model.AuditActionCommentCreate, "", nil, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validCommentStatus(status model.CommentStatus) bool {
	return status == model.CommentStatusPending ||
		status == model.CommentStatusApproved ||
		status == model.CommentStatusRejected
}

func (s *CommentSrv) Moderate(
	ctx context.Context,
	actorID, id, version int64,
	status model.CommentStatus,
	reason string,
) (*model.Comment, error) {
	if !validCommentStatus(status) {
		return nil, errorx.ErrInvalidArgument
	}
	return s.change(ctx, actorID, id, version, model.AuditActionCommentModerate, status, reason)
}

func (s *CommentSrv) Delete(ctx context.Context, actorID, id, version int64, reason string) error {
	_, err := s.change(ctx, actorID, id, version, model.AuditActionCommentDelete, "", reason)
	return err
}

func (s *CommentSrv) change(
	ctx context.Context,
	actorID, id, version int64,
	action string,
	status model.CommentStatus,
	reason string,
) (*model.Comment, error) {
	reason = strings.TrimSpace(reason)
	if actorID <= 0 || id <= 0 || version <= 0 || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 500 {
		return nil, errorx.ErrInvalidArgument
	}
	var result *model.Comment
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r := generated.CommentRepo[model.Comment](tx)
		before, err := repo.One(r.GetByIDForUpdate(ctx, id))
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errorx.ErrNotFound
		}
		if err != nil {
			return err
		}
		if before.DeletedAt != nil || before.Version != version {
			return errorx.ErrConflict
		}
		var rows []model.Comment
		if action == model.AuditActionCommentDelete {
			rows, err = r.SoftDelete(ctx, id, version)
		} else {
			rows, err = r.Moderate(ctx, id, version, status, actorID, reason)
		}
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errorx.ErrConflict
		}
		result = &rows[0]
		return insertCommentAudit(ctx, tx, actorID, action, reason, before, result)
	}); err != nil {
		return nil, err
	}
	return result, nil
}

type commentAuditSnapshot struct {
	Status           model.CommentStatus `json:"status"`
	Version          int64               `json:"version"`
	DeletedAt        *time.Time          `json:"deleted_at"`
	ReviewedBy       *int64              `json:"reviewed_by,string"`
	ReviewedAt       *time.Time          `json:"reviewed_at"`
	ModerationReason string              `json:"moderation_reason"`
}

func encodeCommentSnapshot(c *model.Comment) (datatypes.JSON, error) {
	if c == nil {
		return nil, nil
	}
	b, err := json.Marshal(commentAuditSnapshot{
		Status:           c.Status,
		Version:          c.Version,
		DeletedAt:        c.DeletedAt,
		ReviewedBy:       c.ReviewedBy,
		ReviewedAt:       c.ReviewedAt,
		ModerationReason: c.ModerationReason,
	})
	return datatypes.JSON(b), err
}
func insertCommentAudit(ctx context.Context, tx *gorm.DB, actorID int64, action, reason string, before, after *model.Comment) error {
	b, err := encodeCommentSnapshot(before)
	if err != nil {
		return err
	}
	a, err := encodeCommentSnapshot(after)
	if err != nil {
		return err
	}
	return generated.AuditLogRepo[model.AuditLog](tx).Insert(
		ctx,
		model.AuditLog{
			ActorUserID:  actorID,
			ResourceType: model.AuditResourceComment,
			ResourceID:   strconv.FormatInt(after.ID, 10),
			Action:       action,
			BeforeData:   b,
			AfterData:    a,
			Reason:       reason,
		},
	)
}

type PublicCommentPage struct {
	Comments        []model.PublicComment `json:"comments"`
	NextCursor      *string               `json:"next_cursor"`
	CommentsEnabled bool                  `json:"comments_enabled"`
}
type AdminCommentPage struct {
	Comments   []model.Comment `json:"comments"`
	NextCursor *string         `json:"next_cursor"`
}

func commentPageSize(limit int) (int, error) {
	if limit == 0 {
		return 20, nil
	}
	if limit < 1 || limit > 100 {
		return 0, errorx.ErrInvalidArgument
	}
	return limit, nil
}

func (s *CommentSrv) ListPublic(
	ctx context.Context,
	key string,
	parentID *int64,
	afterID int64,
	limit int,
) (*PublicCommentPage, error) {
	key, err := commentArticleKey(key)
	if err != nil {
		return nil, err
	}
	limit, err = commentPageSize(limit)
	if err != nil {
		return nil, err
	}
	if afterID < 0 || (parentID != nil && *parentID <= 0) {
		return nil, errorx.ErrInvalidArgument
	}
	enabled, err := s.requireArticleReadable(ctx, key)
	if err != nil {
		return nil, err
	}
	r := generated.CommentRepo[model.Comment](s.db)
	var rows []model.PublicComment
	if parentID == nil {
		rows, err = r.ListPublicRoots(ctx, key, afterID, limit+1)
	} else {
		parent, e := repo.One(r.GetByID(ctx, *parentID))
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, errorx.ErrNotFound
		}
		if e != nil {
			return nil, e
		}
		if parent.ArticleKey != key || parent.ParentID != nil || parent.Status != model.CommentStatusApproved {
			return nil, errorx.ErrNotFound
		}
		rows, err = r.ListPublicReplies(ctx, key, *parentID, afterID, limit+1)
	}
	if err != nil {
		return nil, err
	}
	page := &PublicCommentPage{CommentsEnabled: enabled}
	if len(rows) > limit {
		cursor := strconv.FormatInt(rows[limit-1].ID, 10)
		page.NextCursor = &cursor
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []model.PublicComment{}
	}
	page.Comments = rows
	return page, nil
}
func (s *CommentSrv) ListReplies(ctx context.Context, parentID, afterID int64, limit int) (*PublicCommentPage, error) {
	if parentID <= 0 {
		return nil, errorx.ErrInvalidArgument
	}
	parent, err := repo.One(generated.CommentRepo[model.Comment](s.db).GetByID(ctx, parentID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errorx.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.ListPublic(ctx, parent.ArticleKey, &parentID, afterID, limit)
}
func (s *CommentSrv) ListForAdmin(
	ctx context.Context,
	status model.CommentStatus,
	articleKey *string,
	beforeID int64,
	limit int,
) (*AdminCommentPage, error) {
	if beforeID < 0 || (status != "" && !validCommentStatus(status)) {
		return nil, errorx.ErrInvalidArgument
	}
	limit, err := commentPageSize(limit)
	if err != nil {
		return nil, err
	}
	if articleKey != nil {
		key, e := commentArticleKey(*articleKey)
		if e != nil {
			return nil, e
		}
		articleKey = &key
	}
	rows, err := generated.CommentRepo[model.Comment](s.db).ListForAdmin(ctx, status, articleKey, beforeID, limit+1)
	if err != nil {
		return nil, err
	}
	page := &AdminCommentPage{}
	if len(rows) > limit {
		cursor := strconv.FormatInt(rows[limit-1].ID, 10)
		page.NextCursor = &cursor
		rows = rows[:limit]
	}
	if rows == nil {
		rows = []model.Comment{}
	}
	page.Comments = rows
	return page, nil
}
