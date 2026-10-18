package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
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
	if len(missingNames) > 0 {
		return serverSettings{}, fmt.Errorf("missing settings: %s", strings.Join(missingNames, ", "))
	}
	if settings.httpPort == "" {
		settings.httpPort = defaultHTTPPort
	}
	return settings, nil
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
