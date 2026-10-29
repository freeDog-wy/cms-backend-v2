package srv

import (
	"context"
	"fmt"

	"github.com/freeDog-wy/cms-backend-v2/internal/model"
	"github.com/freeDog-wy/cms-backend-v2/internal/repo/generated"
	"gorm.io/gorm"
)

type SessionSrv struct {
	db *gorm.DB
}

func NewSessionSrv(db *gorm.DB) *SessionSrv {
	return &SessionSrv{
		db: db,
	}
}

// CleanupExpired 分批清理过期会话，每批独立提交，每轮最多删除 5000 条。
func (s *SessionSrv) CleanupExpired(ctx context.Context) (int64, error) {
	const (
		batchSize  = 500
		maxBatches = 10
	)

	var total int64
	for range maxBatches {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		deleted, err := generated.SessionRepo[model.Session](s.db).DeleteExpiredBatch(ctx, batchSize)
		if err != nil {
			return total, fmt.Errorf("delete expired sessions: %w", err)
		}
		total += deleted
		if deleted < batchSize {
			break
		}
	}
	return total, nil
}
