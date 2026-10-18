package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

const sendTimeout = 30 * time.Second

type SMTPSettings struct {
	Host       string
	Port       string
	Username   string
	Password   string
	FromEmail  string
	SenderName string
	Encryption string
}

type SMTPSender struct {
	settings SMTPSettings
}

func NewSMTPSender(settings SMTPSettings) SMTPSender {
	return SMTPSender{settings: settings}
}

func (sender SMTPSender) Send(ctx context.Context, toEmail string, subject string, body string) error {
	if strings.ContainsAny(toEmail, "\r\n") {
		return errors.New("send email: recipient contains a line break")
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	connection, err := sender.connect(ctx)
	if err != nil {
		return fmt.Errorf("connect to smtp server: %w", err)
	}
	deadline, _ := ctx.Deadline()
	connection.SetDeadline(deadline)

	client, err := smtp.NewClient(connection, sender.settings.Host)
	if err != nil {
		return describeSMTPError("greet smtp server", err)
	}
	defer client.Close()

	if !sender.usesImplicitTLS() {
		if offersStartTLS, _ := client.Extension("STARTTLS"); offersStartTLS {
			if err := client.StartTLS(sender.tlsConfig()); err != nil {
				return describeSMTPError("start tls", err)
			}
		}
	}
	if sender.settings.Username != "" && sender.settings.Password != "" {
		auth := smtp.PlainAuth("", sender.settings.Username, sender.settings.Password, sender.settings.Host)
		if err := client.Auth(auth); err != nil {
			return describeSMTPError("log in to smtp server", err)
		}
	}
	if err := client.Mail(sender.settings.FromEmail); err != nil {
		return describeSMTPError("set sender", err)
	}
	if err := client.Rcpt(toEmail); err != nil {
		return describeSMTPError("set recipient", err)
	}
	messageWriter, err := client.Data()
	if err != nil {
		return describeSMTPError("start message", err)
	}
	if _, err := messageWriter.Write(sender.buildMessage(toEmail, subject, body)); err != nil {
		return describeSMTPError("write message", err)
	}
	if err := messageWriter.Close(); err != nil {
		return describeSMTPError("finish message", err)
	}
	if err := client.Quit(); err != nil {
		return describeSMTPError("quit smtp session", err)
	}
	return nil
}

func (sender SMTPSender) connect(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(sender.settings.Host, sender.settings.Port)
	if sender.usesImplicitTLS() {
		tlsDialer := tls.Dialer{Config: sender.tlsConfig()}
		return tlsDialer.DialContext(ctx, "tcp", address)
	}
	var plainDialer net.Dialer
	return plainDialer.DialContext(ctx, "tcp", address)
}

func (sender SMTPSender) usesImplicitTLS() bool {
	encryption := strings.ToLower(sender.settings.Encryption)
	return encryption == "tls" || encryption == "ssl"
}

func (sender SMTPSender) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: sender.settings.Host, MinVersion: tls.VersionTLS12}
}

func (sender SMTPSender) buildMessage(toEmail string, subject string, body string) []byte {
	from := mail.Address{Name: sender.settings.SenderName, Address: sender.settings.FromEmail}
	header := "Date: " + time.Now().Format(time.RFC1123Z) + "\n" +
		"From: " + from.String() + "\n" +
		"To: " + toEmail + "\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\n" +
		"MIME-Version: 1.0\n" +
		"Content-Type: text/plain; charset=utf-8\n\n"
	return []byte(header + body)
}

func describeSMTPError(step string, err error) error {
	var serverError *textproto.Error
	if errors.As(err, &serverError) {
		return fmt.Errorf("%s: smtp server answered with code %d", step, serverError.Code)
	}
	return fmt.Errorf("%s: %w", step, err)
}
