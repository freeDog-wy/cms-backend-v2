package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // Embed timezone data for minimal container images.

	"github.com/freeDog-wy/cms-backend-v2/internal/bootstrap"
	"github.com/freeDog-wy/cms-backend-v2/internal/config"
	"github.com/freeDog-wy/cms-backend-v2/internal/event"
	httpapi "github.com/freeDog-wy/cms-backend-v2/internal/hdl/http_api"
	httpmiddleware "github.com/freeDog-wy/cms-backend-v2/internal/hdl/http_middleware"
	"github.com/freeDog-wy/cms-backend-v2/internal/infra"
	"github.com/freeDog-wy/cms-backend-v2/internal/job"
	"github.com/freeDog-wy/cms-backend-v2/internal/migration"
	"github.com/freeDog-wy/cms-backend-v2/internal/pkg"
	"github.com/freeDog-wy/cms-backend-v2/internal/srv"
	"github.com/freeDog-wy/cms-backend-v2/internal/task"
	"github.com/gin-gonic/gin"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	emailSender, err := newEmailSender(cfg)
	if err != nil {
		log.Fatalf("initialize email sender: %v", err)
	}
	log.Printf("Email sender initialized: %T", emailSender)

	log.Printf("Bootstrap Mode: %s\n", cfg.Mode)

	if err := migration.Up(cfg.DatabaseURL); err != nil {
		log.Fatal(err)
	}

	db, err := infra.NewDB(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}

	workerPool, err := infra.NewWorkerPool()
	if err != nil {
		log.Fatal(err)
	}

	scheduler, err := infra.NewScheduler()
	if err != nil {
		log.Fatal(err)
	}

	passwordHasher, err := pkg.NewArgon2Hasher(5)
	if err != nil {
		log.Fatal(err)
	}

	if cfg.Mode == "PROD" || cfg.Mode == "TEST" {
		gin.SetMode(gin.ReleaseMode)
	}

	outboxSrv := srv.NewOutboxSrv(db, workerPool, 40*time.Second)
	sessionSrv := srv.NewSessionSrv(db)
	authSrv := srv.NewAuthSrv(db, passwordHasher)
	rbacSrv := srv.NewRBACSrv(db)
	commentSrv := srv.NewCommentSrv(db, cfg.TrustedOriginURL)

	// 首次加载失败不阻止登录和其他业务；评论接口在加载成功前返回 503。
	if err := commentSrv.RefreshManifest(context.Background()); err != nil {
		log.Printf("initial comment manifest: %v", err)
	}

	// business init
	if err := bootstrap.AdminInit(context.Background(), authSrv, rbacSrv, cfg); err != nil {
		log.Fatal(err)
	}

	// task
	commentReviewTask, err := task.NewCommentReviewTask(db, emailSender, cfg.AdminEmail)
	if err != nil {
		log.Fatal(err)
	}
	if err := workerPool.RegisterTask(event.CommentSubmitted, commentReviewTask.Handle); err != nil {
		log.Fatal(err)
	}

	// job
	// 所有 Job 共享生命周期上下文，退出时统一取消。
	jobsCtx, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()
	if _, err := scheduler.AddJob("@every 5m", job.NewCommentManifestJob(jobsCtx, commentSrv)); err != nil {
		log.Fatal(err)
	}
	if _, err := scheduler.AddJob("@every 10s", job.NewOutboxHandleJob(jobsCtx, outboxSrv)); err != nil {
		log.Fatal(err)
	}
	if _, err := scheduler.AddJob("@every 30m", job.NewSessionCleanupJob(jobsCtx, sessionSrv)); err != nil {
		log.Fatal(err)
	}
	scheduler.Start()

	// http
	engine := NewGinEngine(cfg)

	authHdl := httpapi.NewAuthHdl(authSrv, rbacSrv)
	commentHdl := httpapi.NewCommentHdl(commentSrv, authSrv, rbacSrv)

	authHdl.RegisterRoutes(engine)
	commentHdl.RegisterRoutes(engine)

	httpServer := &http.Server{
		Addr:    ":8080",
		Handler: engine,
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)

	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// shutdown
	<-quit
	done := scheduler.Stop()
	cancelJobs()

	log.Println("Shutdown Server ...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Println("Server Shutdown: ", err)
	}
	log.Println("Server exited")

	select {
	case <-done.Done():
		log.Println("scheduler done")
	case <-time.After(3 * time.Second):
		log.Print("scheduler shutdown timeout")
	}

	if err := workerPool.ReleaseTimeout(context.Background(), 3*time.Second); err != nil {
		log.Println("worker pool shutdown: ", err)
	}
	log.Println("worker pool exited")
}

func newEmailSender(cfg *config.Config) (pkg.EmailSender, error) {
	switch cfg.EmailDriver {
	case "log":
		if cfg.Mode == "PROD" {
			return nil, fmt.Errorf("PROD requires SMTP email sender")
		}
		from := cfg.SMTP.From
		if from == "" {
			from = "CMS <noreply@localhost>"
		}
		return pkg.NewLogEmailSender(from, nil)
	case "smtp":
		return pkg.NewSMTPEmailSender(cfg.SMTP)
	default:
		return nil, fmt.Errorf("email driver must be log or smtp")
	}
}

func NewGinEngine(cfg *config.Config) *gin.Engine {
	csrf := http.NewCrossOriginProtection() // 前后端跨源部署时，明确添加可信前端来源。
	if err := csrf.AddTrustedOrigin(cfg.TrustedOriginURL); err != nil {
		log.Fatal(err)
	}
	engine := gin.New()

	// 只信任 cloudflare 的代理转发请求头，不信任其他代理请求头
	// 确保服务只能在本机访问，然后由cloudfalred代理服务器向本后端转发请求
	engine.TrustedPlatform = gin.PlatformCloudflare

	// // 没有配置可信代理时，空切片/nil 表示不信任转发头。
	// if err := engine.SetTrustedProxies(cfg.TrustedProxiesIP); err != nil {
	// 	log.Fatal(err)
	// }
	// // 只有连接来源属于可信代理时，才解析这个请求头。
	// engine.ForwardedByClientIP = true
	// engine.RemoteIPHeaders = cfg.RemoteIPHeaders

	engine.Use(gin.Logger())
	engine.Use(gin.Recovery())
	engine.Use(httpmiddleware.Csrf(csrf))

	return engine
}
