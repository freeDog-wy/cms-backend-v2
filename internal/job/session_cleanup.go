package job

import (
	"context"
	"log"
	"time"
)

type sessionCleaner interface {
	CleanupExpired(ctx context.Context) (int64, error)
}

type SessionCleanupJob struct {
	ctx     context.Context
	cleaner sessionCleaner
}

func NewSessionCleanupJob(ctx context.Context, cleaner sessionCleaner) *SessionCleanupJob {
	return &SessionCleanupJob{ctx: ctx, cleaner: cleaner}
}

func (job *SessionCleanupJob) Run() {
	// 超时限制每轮执行时间，父上下文用于服务退出时取消任务。
	ctx, cancel := context.WithTimeout(job.ctx, 30*time.Second)
	defer cancel()

	deleted, err := job.cleaner.CleanupExpired(ctx)
	if err != nil {
		log.Printf("session cleanup failed after deleting %d rows: %v", deleted, err)
		return
	}
	if deleted > 0 {
		log.Printf("session cleanup deleted %d expired sessions", deleted)
	}
}
