package srv

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/event"
	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/pkg"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo/generated"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type AuthSrv struct {
	db             *gorm.DB
	passwordHasher pkg.PasswordHasher

	loginConcurrency pkg.KeyConcurrencyLimiter // 按照用户名做并发限制，一个用户同时只能并发请求一个登录请求
	loginCooldown    pkg.KeyCooldown           // 5分钟内，用户登录5次失败，则限流，并冷却1分钟
}

func NewAuthSrv(db *gorm.DB, passwordHasher pkg.PasswordHasher) *AuthSrv {
	return &AuthSrv{
		db:               db,
		passwordHasher:   passwordHasher,
		loginConcurrency: pkg.NewMemoryConcurrencyLimiter(1, 10000),
		loginCooldown:    pkg.NewMemoryCooldown(5, 5*time.Minute, time.Minute),
	}
}

type UserRegisterCmd struct {
	Username string  `json:"username"`
	Nickname string  `json:"nickname"`
	Email    *string `json:"email"`
	Password string  `json:"password"`
}

func (auth *AuthSrv) Register(ctx context.Context, userReq UserRegisterCmd) (*model.User, error) {

	pwdhash, err := auth.passwordHasher.Hash(ctx, userReq.Password)
	if err != nil {
		if errors.Is(err, pkg.ErrPasswordHasherBusy) {
			return nil, errorx.ErrServiceBusy
		}
		return nil, err
	}

	user := model.User{
		Username:     userReq.Username,
		Nickname:     userReq.Nickname,
		Email:        userReq.Email,
		PasswordHash: pwdhash,
		Status:       model.UserStatusEnabled,
	}

	var ans *model.User
	if err = auth.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err = repo.One(generated.UserRepo[model.User](tx).GetByUsername(ctx, user.Username))
		if err == nil {
			return errorx.ErrUsernameExists
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		ans, err = generated.UserRepo[model.User](tx).InsertUser(ctx, user)
		if err != nil {
			return repo.MapUserError(err)
		}
		if err = generated.OutboxRepo[model.OutboxMessage](tx).InsertMessage(ctx,
			event.UserRegistered,
			datatypes.JSON([]byte(
				`{"user_id": 123}`,
			)),
		); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return ans, nil
}

type UserLoginCmd struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type UserLoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (auth *AuthSrv) Login(ctx context.Context, userReq UserLoginCmd) (*UserLoginResult, error) {
	// 规范化规则必须与账号查询规则保持一致。
	key := "login:account:" + strings.ToLower(userReq.Username)

	release, acquired := auth.loginConcurrency.TryAcquire(key)
	if !acquired {
		return nil, errorx.ErrTooManyRequest
	}
	defer release()

	if wait := auth.loginCooldown.Check(key); wait > 0 {
		return nil, errorx.ErrTooManyRequest
	}

	result, err := auth.login(ctx, userReq)

	switch {
	case err == nil:
		auth.loginCooldown.Reset(key)

	case errors.Is(err, errorx.ErrInvalidCredentials):
		auth.loginCooldown.Hit(key)
	}

	return result, err
}

func (auth *AuthSrv) login(ctx context.Context, userReq UserLoginCmd) (*UserLoginResult, error) {
	var token *string
	var expires_at time.Time = time.Now().Add(24 * time.Hour)

	user, err := repo.One(generated.UserRepo[model.User](auth.db).GetByUsername(ctx, userReq.Username))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errorx.ErrInvalidCredentials
		}
		return nil, err
	}

	if user.Status == model.UserStatusDisabled {
		return nil, errorx.ErrUserDisabled
	}

	match, err := auth.passwordHasher.Verify(ctx, userReq.Password, user.PasswordHash)
	if err != nil {
		if errors.Is(err, pkg.ErrPasswordHasherBusy) {
			return nil, errorx.ErrServiceBusy
		}
		return nil, err
	}
	if !match {
		return nil, errorx.ErrInvalidCredentials
	}

	var digest []byte
	token, digest, err = pkg.GenerateToken()
	if err != nil {
		return nil, err
	}

	if err := auth.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := repo.One(generated.UserRepo[model.User](tx).GetByUsername(ctx, userReq.Username))
		if err != nil {
			return err
		}
		if _, err := generated.SessionRepo[model.Session](tx).InsertSession(ctx, model.Session{
			UserID:    user.ID,
			TokenHash: digest,
			ExpiresAt: expires_at,
		}); err != nil {
			return err
		}
		if err := generated.UserRepo[model.User](tx).UpdateLoginTimeByID(ctx, user.ID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &UserLoginResult{
		Token:     *token,
		ExpiresAt: expires_at,
	}, nil
}
