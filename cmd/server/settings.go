package main

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/emberr-app/emberr-server/internal/auth"
)

const defaultHTTPPort = "8080"

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
	}
	encodedEmailEncryptionKey := readRequired("EMBERR_KEY_ENCRYPTION")
	encodedEmailHashKey := readRequired("EMBERR_KEY_HASH")
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
	settings.emailEncryptionKey = [auth.EmailEncryptionKeyLength]byte(emailEncryptionKey)
	settings.emailHashKey = [auth.EmailHashKeyLength]byte(emailHashKey)

	if settings.httpPort == "" {
		settings.httpPort = defaultHTTPPort
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
