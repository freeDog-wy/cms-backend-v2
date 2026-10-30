package job

import (
	"context"
	"log"
	"time"

	"github.com/freeDog-wy/cms-backend-v2/internal/srv"
)

type CommentManifestJob struct {
	ctx        context.Context
	commentSrv *srv.CommentSrv
}

func NewCommentManifestJob(ctx context.Context, commentSrv *srv.CommentSrv) *CommentManifestJob {
	return &CommentManifestJob{ctx: ctx, commentSrv: commentSrv}
}
func (j *CommentManifestJob) Run() {
	ctx, cancel := context.WithTimeout(j.ctx, 10*time.Second)
	defer cancel()
	if err := j.commentSrv.RefreshManifest(ctx); err != nil {
		log.Printf("refresh comment manifest: %v", err)
	}
}
