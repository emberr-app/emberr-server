package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/emberr-app/emberr-server/internal/api"
	"github.com/emberr-app/emberr-server/internal/auth"
	"github.com/emberr-app/emberr-server/internal/email"
)

const (
	defaultHTTPPort       = "8080"
	defaultSMTPSenderName = "Emberr"
)

type serverSettings struct {
	httpPort         string
	databaseHost     string
	databasePort     string
	databaseName     string
	databaseUser     string
	databasePassword string
	databaseSSLMode  string

	emailEncryptionKey [auth.EmailEncryptionKeyLength]byte
	emailHashKey       [auth.EmailHashKeyLength]byte
	fakeSaltKey        [auth.FakeSaltKeyLength]byte

	minimumAppVersion api.AppVersion

	smtp email.SMTPSettings
}

func loadSettings() (serverSettings, error) {
	var missingNames []string
	readRequired := func(name string) string {
		value := os.Getenv(name)
		if value == "" {
			missingNames = append(missingNames, name)
		}
		return value
	}

	settings := serverSettings{
		httpPort:         os.Getenv("EMBERR_HTTP_PORT"),
		databaseHost:     readRequired("EMBERR_DB_HOST"),
		databasePort:     readRequired("EMBERR_DB_PORT"),
		databaseName:     readRequired("EMBERR_DB_NAME"),
		databaseUser:     readRequired("EMBERR_DB_USER"),
		databasePassword: readRequired("EMBERR_DB_PASSWORD"),
		databaseSSLMode:  readRequired("EMBERR_DB_SSLMODE"),
		smtp: email.SMTPSettings{
			Host:       readRequired("EMBERR_SMTP_HOST"),
			Port:       readRequired("EMBERR_SMTP_PORT"),
			Username:   os.Getenv("EMBERR_SMTP_USERNAME"),
			Password:   os.Getenv("EMBERR_SMTP_PASSWORD"),
			FromEmail:  readRequired("EMBERR_SMTP_EMAIL"),
			SenderName: os.Getenv("EMBERR_SMTP_SENDER_NAME"),
			Encryption: os.Getenv("EMBERR_SMTP_ENCRYPTION"),
		},
	}
	encodedEmailEncryptionKey := readRequired("EMBERR_KEY_ENCRYPTION")
	encodedEmailHashKey := readRequired("EMBERR_KEY_HASH")
	encodedFakeSaltKey := readRequired("EMBERR_KEY_FAKE_SALT")
	if len(missingNames) > 0 {
		return serverSettings{}, fmt.Errorf("missing settings: %s", strings.Join(missingNames, ", "))
	}

	emailEncryptionKey, err := decodeBase64Key("EMBERR_KEY_ENCRYPTION", encodedEmailEncryptionKey, auth.EmailEncryptionKeyLength)
	if err != nil {
		return serverSettings{}, err
	}
	emailHashKey, err := decodeBase64Key("EMBERR_KEY_HASH", encodedEmailHashKey, auth.EmailHashKeyLength)
	if err != nil {
		return serverSettings{}, err
	}
	fakeSaltKey, err := decodeBase64Key("EMBERR_KEY_FAKE_SALT", encodedFakeSaltKey, auth.FakeSaltKeyLength)
	if err != nil {
		return serverSettings{}, err
	}
	settings.emailEncryptionKey = [auth.EmailEncryptionKeyLength]byte(emailEncryptionKey)
	settings.emailHashKey = [auth.EmailHashKeyLength]byte(emailHashKey)
	settings.fakeSaltKey = [auth.FakeSaltKeyLength]byte(fakeSaltKey)

	if minimumAppVersion := os.Getenv("EMBERR_MIN_APP_VERSION"); minimumAppVersion != "" {
		parsedVersion, valid := api.ParseAppVersion(minimumAppVersion)
		if !valid {
			return serverSettings{}, fmt.Errorf("EMBERR_MIN_APP_VERSION must look like 1.2.1, got %q", minimumAppVersion)
		}
		settings.minimumAppVersion = parsedVersion
	}

	if settings.httpPort == "" {
		settings.httpPort = defaultHTTPPort
	}
	if settings.smtp.SenderName == "" {
		settings.smtp.SenderName = defaultSMTPSenderName
	}
	return settings, nil
}

func decodeBase64Key(settingName string, encodedKey string, wantLength int) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64: %w", settingName, err)
	}
	if len(key) != wantLength {
		return nil, fmt.Errorf("%s must be %d bytes, got %d", settingName, wantLength, len(key))
	}
	return key, nil
}

func (settings serverSettings) databaseURL() string {
	address := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(settings.databaseUser, settings.databasePassword),
		Host:     net.JoinHostPort(settings.databaseHost, settings.databasePort),
		Path:     settings.databaseName,
		RawQuery: url.Values{"sslmode": {settings.databaseSSLMode}}.Encode(),
	}
	return address.String()
}
