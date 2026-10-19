package testutil

import (
	"context"
	"sync"
	"testing"
	"time"
)

const mailWaitLimit = 5 * time.Second

type SentMail struct {
	ToEmail string
	Subject string
	Body    string
}

type FakeMailSender struct {
	lock      sync.Mutex
	sentMails []SentMail
}

func (sender *FakeMailSender) Send(ctx context.Context, toEmail string, subject string, body string) error {
	sender.lock.Lock()
	defer sender.lock.Unlock()
	sender.sentMails = append(sender.sentMails, SentMail{ToEmail: toEmail, Subject: subject, Body: body})
	return nil
}

func (sender *FakeMailSender) SentMails() []SentMail {
	sender.lock.Lock()
	defer sender.lock.Unlock()
	return append([]SentMail(nil), sender.sentMails...)
}

func (sender *FakeMailSender) WaitForMails(t *testing.T, toEmail string, subject string) []SentMail {
	t.Helper()

	deadline := time.Now().Add(mailWaitLimit)
	for {
		var matchingMails []SentMail
		for _, sentMail := range sender.SentMails() {
			if sentMail.ToEmail == toEmail && sentMail.Subject == subject {
				matchingMails = append(matchingMails, sentMail)
			}
		}
		if len(matchingMails) > 0 {
			return matchingMails
		}
		if time.Now().After(deadline) {
			t.Fatalf("no email with subject %q arrived within %s", subject, mailWaitLimit)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
