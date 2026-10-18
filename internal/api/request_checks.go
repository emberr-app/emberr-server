package api

import (
	"encoding/base64"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/emberr-app/emberr-server/internal/auth"
)

const (
	maxEmailLength      = 254
	maxDeviceNameLength = 100
)

func checkEmail(rawEmail string) (string, error) {
	email := auth.NormalizeEmail(rawEmail)
	if email == "" {
		return "", newBadRequestError("email is missing")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > maxEmailLength {
		return "", newBadRequestError("email is not a valid address")
	}
	return email, nil
}

func checkDeviceName(deviceName string) error {
	if strings.TrimSpace(deviceName) == "" {
		return newBadRequestError("deviceName is missing")
	}
	if utf8.RuneCountInString(deviceName) > maxDeviceNameLength {
		return newBadRequestError(fmt.Sprintf("deviceName must be at most %d characters", maxDeviceNameLength))
	}
	return nil
}

func decodeBase64Field(fieldName string, value string, wantLength int) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != wantLength || base64.StdEncoding.EncodeToString(decoded) != value {
		return nil, newBadRequestError(fmt.Sprintf("%s must be %d bytes in standard base64", fieldName, wantLength))
	}
	return decoded, nil
}

func checkLoginFields(rawEmail string, encodedLoginKey string, deviceName string) (string, []byte, error) {
	email, err := checkEmail(rawEmail)
	if err != nil {
		return "", nil, err
	}
	loginKey, err := decodeBase64Field("loginKey", encodedLoginKey, auth.LoginKeyLength)
	if err != nil {
		return "", nil, err
	}
	if err := checkDeviceName(deviceName); err != nil {
		return "", nil, err
	}
	return email, loginKey, nil
}
