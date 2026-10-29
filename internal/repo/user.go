package repo

import (
	"context"
	"errors"

	"github.com/freeDog-wy/cms-backend-v2/internal/errorx"
	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/jackc/pgx/v5/pgconn"
)

//go:generate go run gorm.io/cli/gorm gen -i $GOFILE -o ./generated

type UserRepo[T any] interface {

	// SELECT * FROM @@table WHERE id=@id
	GetByID(ctx context.Context, id int64) ([]T, error)

	// SELECT * FROM @@table WHERE lower(username) = lower(@username)
	GetByUsername(ctx context.Context, username string) ([]T, error)

	// UPDATE @@table
	// SET last_login_at = CURRENT_TIMESTAMP
	// WHERE id = @id
	UpdateLoginTimeByID(ctx context.Context, id int64) error

	// INSERT INTO @@table (
	//     username, nickname, email, password_hash, status
	// )
	// VALUES (
	//     @user.Username, @user.Nickname, @user.Email,
	//     @user.PasswordHash, @user.Status
	// )
	// RETURNING *
	InsertUser(ctx context.Context, user model.User) (*T, error)
}

func MapUserError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return err
	}

	switch pgErr.ConstraintName {
	case "ux_users_username_lower":
		return errorx.ErrUsernameExists
	case "ux_users_email_lower":
		return errorx.ErrEmailExists
	default:
		return err
	}
}
