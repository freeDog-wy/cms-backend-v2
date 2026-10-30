package srv

import (
	"context"

	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo/generated"
	"gorm.io/gorm"
)

const PermissionCommentModerate = "comment:moderate"

type RBACSrv struct{ db *gorm.DB }

func NewRBACSrv(db *gorm.DB) *RBACSrv { return &RBACSrv{db: db} }

func (s *RBACSrv) HasPermission(ctx context.Context, userID int64, permission string) (bool, error) {
	return generated.RBACRepo[model.Role](s.db).HasPermission(ctx, userID, permission)
}

func (s *RBACSrv) ListPermissions(ctx context.Context, userID int64) ([]string, error) {
	return generated.RBACRepo[model.Role](s.db).ListPermissions(ctx, userID)
}

// EnsureAdmin 在服务启动时补充授权；权限规则不写入迁移。
func (s *RBACSrv) EnsureAdmin(ctx context.Context, username string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := repo.One(generated.UserRepo[model.User](tx).GetByUsername(ctx, username))
		if err != nil {
			return err
		}
		r := generated.RBACRepo[model.Role](tx)
		if err := r.EnsureAdminRole(ctx); err != nil {
			return err
		}
		if err := r.EnsureCommentPermission(ctx); err != nil {
			return err
		}
		if err := r.GrantAdminCommentPermission(ctx); err != nil {
			return err
		}
		return r.AssignAdmin(ctx, user.ID)
	})
}
