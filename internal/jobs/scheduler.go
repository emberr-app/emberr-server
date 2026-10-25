package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	timer             *cron.Cron
	cancelRunningJobs context.CancelFunc
}

type scheduledJob struct {
	name     string
	schedule string
	run      func(ctx context.Context) error
}

func StartScheduler(ctx context.Context, cleanup *Cleanup) (*Scheduler, error) {
	jobContext, cancelRunningJobs := context.WithCancel(ctx)
	timer := cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))

	scheduledJobs := []scheduledJob{
		{name: "delete due objects", schedule: "@every 8m", run: cleanup.DeleteDueObjects},
		{name: "queue expired pending uploads", schedule: "@every 5m", run: cleanup.QueueExpiredPendingUploads},
		{name: "remove expired codes and rate limits", schedule: "@every 1m", run: cleanup.RemoveExpiredCodesAndRateLimits},
	}
	for _, job := range scheduledJobs {
		_, err := timer.AddFunc(job.schedule, func() {
			if err := job.run(jobContext); err != nil {
				slog.Error("scheduled job failed", "job", job.name, "error", withoutRequestURL(err))
			}
		})
		if err != nil {
			cancelRunningJobs()
			return nil, fmt.Errorf("schedule %s: %w", job.name, err)
		}
	}

	timer.Start()
	return &Scheduler{timer: timer, cancelRunningJobs: cancelRunningJobs}, nil
}

func (scheduler *Scheduler) Stop(ctx context.Context) error {
	jobsFinished := scheduler.timer.Stop()
	scheduler.cancelRunningJobs()
	select {
	case <-jobsFinished.Done():
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for running jobs: %w", ctx.Err())
	}
}
