package infra

import (
	"log"
	"os"
	"time"

	"github.com/robfig/cron/v3"
)

func NewScheduler() (*cron.Cron, error) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, err
	}

	logger := cron.VerbosePrintfLogger(
		log.New(os.Stdout, "cron: ", log.LstdFlags),
	)

	scheduler := cron.New(
		cron.WithLocation(loc),
		cron.WithLogger(logger),
		cron.WithChain(
			cron.SkipIfStillRunning(logger),
			cron.Recover(logger),
		),
	)
	return scheduler, nil
}
