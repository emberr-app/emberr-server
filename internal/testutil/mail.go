package testutil

import "context"

type SentMail struct {
	ToEmail string
	Subject string
	Body    string
}

type FakeMailSender struct {
	SentMails []SentMail
}

func (sender *FakeMailSender) Send(ctx context.Context, toEmail string, subject string, body string) error {
	sender.SentMails = append(sender.SentMails, SentMail{ToEmail: toEmail, Subject: subject, Body: body})
	return nil
}
