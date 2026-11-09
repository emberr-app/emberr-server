package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/emberr-app/emberr-server/internal/blob"
)

type Scheduler struct {
	timer             *cron.Cron
	cancelRunningJobs context.CancelFunc
	runsAtStart       sync.WaitGroup
}

type scheduledJob struct {
	name       string
	schedule   string
	run        func(ctx context.Context) error
	runAtStart bool
}

func StartScheduler(ctx context.Context, cleanup *Cleanup, trialEnd *TrialEnd) (*Scheduler, error) {
	jobContext, cancelRunningJobs := context.WithCancel(ctx)
	timer := cron.New(cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))

	scheduledJobs := []scheduledJob{
		{name: "delete due objects", schedule: "@every 8m", run: cleanup.DeleteDueObjects},
		{name: "queue expired pending uploads", schedule: "@every 5m", run: cleanup.QueueExpiredPendingUploads},
		{name: "queue expired media uploads", schedule: "@every 5m", run: cleanup.QueueExpiredMediaUploads},
		{name: "queue unused media files", schedule: "@every 1h", run: cleanup.QueueUnusedMediaFiles},
		{name: "remove expired codes and rate limits", schedule: "@every 1m", run: cleanup.RemoveExpiredCodesAndRateLimits},
		{name: "warn and remove ended trials", schedule: "@every 24h", run: trialEnd.WarnAndRemoveEndedTrials, runAtStart: true},
	}
	var jobsToRunAtStart []cron.Job
	for _, job := range scheduledJobs {
		entryID, err := timer.AddFunc(job.schedule, func() {
			if err := job.run(jobContext); err != nil {
				slog.Error("scheduled job failed", "job", job.name, "error", blob.WithoutRequestURL(err))
			}
		})
		if err != nil {
			cancelRunningJobs()
			return nil, fmt.Errorf("schedule %s: %w", job.name, err)
		}
		if job.runAtStart {
			jobsToRunAtStart = append(jobsToRunAtStart, timer.Entry(entryID).WrappedJob)
		}
	}

	timer.Start()
	scheduler := &Scheduler{timer: timer, cancelRunningJobs: cancelRunningJobs}
	for _, job := range jobsToRunAtStart {
		scheduler.runsAtStart.Go(job.Run)
	}
	return scheduler, nil
}

func (scheduler *Scheduler) Stop(ctx context.Context) error {
	jobsFinished := scheduler.timer.Stop()
	scheduler.cancelRunningJobs()
	runsAtStartFinished := make(chan struct{})
	go func() {
		scheduler.runsAtStart.Wait()
		close(runsAtStartFinished)
	}()
	for _, finished := range []<-chan struct{}{jobsFinished.Done(), runsAtStartFinished} {
		select {
		case <-finished:
		case <-ctx.Done():
			return fmt.Errorf("wait for running jobs: %w", ctx.Err())
		}
	}
	return nil
}
