package bootstrap

import (
	"context"
	"errors"

	"github.com/freeDog-wy/cms-backend-v2/internal/config"
	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/srv"
)

func AdminInit(ctx context.Context, authSrv *srv.AuthSrv, rbacSrv *srv.RBACSrv, cfg *config.Config) error {
	if _, err := authSrv.Register(ctx, srv.UserRegisterCmd{
		Username: cfg.AdminName,
		Nickname: cfg.AdminName,
		Email:    &cfg.AdminEmail,
		Password: cfg.AdminPassword,
	}); err != nil && !errors.Is(err, errorx.ErrUsernameExists) {
		return err
	}

	if err := rbacSrv.EnsureAdmin(ctx, cfg.AdminName); err != nil {
		return err
	}
	return nil
}
