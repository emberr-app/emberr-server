package testutil

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

const mailWaitLimit = 5 * time.Second

var errFakeSendFailed = errors.New("fake mail sender refused the email")

type SentMail struct {
	ToEmail string
	Subject string
	Body    string
}

type FakeMailSender struct {
	lock           sync.Mutex
	sentMails      []SentMail
	emailsThatFail map[string]bool
}

func (sender *FakeMailSender) Send(ctx context.Context, toEmail string, subject string, body string) error {
	sender.lock.Lock()
	defer sender.lock.Unlock()
	if sender.emailsThatFail[toEmail] {
		return errFakeSendFailed
	}
	sender.sentMails = append(sender.sentMails, SentMail{ToEmail: toEmail, Subject: subject, Body: body})
	return nil
}

func (sender *FakeMailSender) FailSendsTo(toEmail string) {
	sender.lock.Lock()
	defer sender.lock.Unlock()
	if sender.emailsThatFail == nil {
		sender.emailsThatFail = make(map[string]bool)
	}
	sender.emailsThatFail[toEmail] = true
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
