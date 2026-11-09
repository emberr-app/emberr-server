package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/store"
)

const (
	oneDay                = 24 * time.Hour
	cloudCopyRemovalDelay = 30 * oneDay
	removalNoticeLength   = 24 * time.Hour
	finalWarningKind      = "trial_ended_day_29"
	removalDateLayout     = "2 January 2006"
)

type trialEndEmail struct {
	kind              string
	sendAfterTrialEnd time.Duration
	subject           string
	openingLine       string
}

var trialEndEmails = []trialEndEmail{
	{
		kind:              "trial_ended_day_0",
		sendAfterTrialEnd: 0,
		subject:           "Your Emberr Cloud trial has ended",
		openingLine:       "Your Emberr Cloud trial has ended.",
	},
	{
		kind:              "trial_ended_day_23",
		sendAfterTrialEnd: 23 * oneDay,
		subject:           "Your Emberr Cloud copy will be removed in 7 days",
		openingLine:       "Your Emberr Cloud trial has ended, and your cloud copy will be removed in 7 days.",
	},
	{
		kind:              finalWarningKind,
		sendAfterTrialEnd: 29 * oneDay,
		subject:           "Your Emberr Cloud copy will be removed tomorrow",
		openingLine:       "Your Emberr Cloud trial has ended, and your cloud copy will be removed tomorrow.",
	},
}

type TrialEnd struct {
	subscriptionTable store.SubscriptionTable
	accountTable      store.AccountTable
	mailSender        auth.MailSender
	emailProtector    auth.EmailProtector
}

func NewTrialEnd(pool *pgxpool.Pool, mailSender auth.MailSender, emailProtector auth.EmailProtector) *TrialEnd {
	return &TrialEnd{
		subscriptionTable: store.NewSubscriptionTable(pool),
		accountTable:      store.NewAccountTable(pool),
		mailSender:        mailSender,
		emailProtector:    emailProtector,
	}
}

func (trialEnd *TrialEnd) WarnAndRemoveEndedTrials(ctx context.Context) error {
	endedTrials, err := trialEnd.subscriptionTable.ListEndedTrialsWithCloudCopy(ctx)
	if err != nil {
		return err
	}

	sentCount, failedSendCount, removedCount, failedRemovalCount := 0, 0, 0, 0
	var lastFailure error
	for _, endedTrial := range endedTrials {
		now := time.Now()
		if email, due := newestDueEmailNotYetSent(endedTrial, now); due {
			if err := trialEnd.sendEmail(ctx, endedTrial, email, now); err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("warn and remove ended trials: %w", ctx.Err())
				}
				failedSendCount++
				lastFailure = err
			} else {
				sentCount++
			}
		}

		if cloudCopyRemovalDue(endedTrial, now) {
			if err := trialEnd.accountTable.RemoveCloudCopy(ctx, endedTrial.AccountID); err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("warn and remove ended trials: %w", ctx.Err())
				}
				failedRemovalCount++
				lastFailure = err
			} else {
				removedCount++
			}
		}
	}

	if sentCount > 0 || removedCount > 0 {
		slog.Info("warned ended trials and removed cloud copies", "emailsSent", sentCount, "cloudCopiesRemoved", removedCount)
	}
	if failedSendCount > 0 || failedRemovalCount > 0 {
		slog.Error("some trial end emails or removals failed, they are tried again on the next run", "failedEmails", failedSendCount, "failedRemovals", failedRemovalCount, "error", lastFailure)
	}
	return nil
}

func (trialEnd *TrialEnd) sendEmail(ctx context.Context, endedTrial store.EndedTrial, email trialEndEmail, now time.Time) error {
	toEmail, err := trialEnd.emailProtector.Decrypt(auth.EncryptedEmail{Ciphertext: endedTrial.EncryptedEmail, Nonce: endedTrial.EncryptedEmailNonce})
	if err != nil {
		return err
	}
	body := trialEndEmailBody(email, removalDate(endedTrial, now))
	if err := trialEnd.mailSender.Send(ctx, toEmail, email.subject, body); err != nil {
		return fmt.Errorf("send trial end email: %w", err)
	}
	return trialEnd.subscriptionTable.RecordEmailSent(ctx, endedTrial.AccountID, email.kind)
}

func newestDueEmailNotYetSent(endedTrial store.EndedTrial, now time.Time) (trialEndEmail, bool) {
	timeSinceTrialEnd := now.Sub(time.UnixMicro(endedTrial.ExpiresAt))
	for _, email := range slices.Backward(trialEndEmails) {
		if timeSinceTrialEnd >= email.sendAfterTrialEnd {
			_, alreadySent := endedTrial.EmailsSentAt[email.kind]
			return email, !alreadySent
		}
	}
	return trialEndEmail{}, false
}

func cloudCopyRemovalDue(endedTrial store.EndedTrial, now time.Time) bool {
	finalWarningSentAt, finalWarningSent := endedTrial.EmailsSentAt[finalWarningKind]
	return finalWarningSent &&
		now.Sub(time.UnixMicro(endedTrial.ExpiresAt)) >= cloudCopyRemovalDelay &&
		now.Sub(time.UnixMicro(finalWarningSentAt)) >= removalNoticeLength
}

func removalDate(endedTrial store.EndedTrial, now time.Time) time.Time {
	removal := time.UnixMicro(endedTrial.ExpiresAt).Add(cloudCopyRemovalDelay)
	if earliestRemoval := now.Add(removalNoticeLength); earliestRemoval.After(removal) {
		return earliestRemoval
	}
	return removal
}

func trialEndEmailBody(email trialEndEmail, removal time.Time) string {
	return email.openingLine + "\n\n" +
		"Your cloud copy is now read-only: your devices can still download from it, but nothing new is uploaded.\n\n" +
		"Your cloud copy will be removed on " + removal.UTC().Format(removalDateLayout) + ". " +
		"The notes on your devices are never touched, and your Emberr account stays.\n"
}
