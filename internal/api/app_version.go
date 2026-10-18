package api

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

const appVersionHeader = "X-Client-Version"

type AppVersion [3]int

func ParseAppVersion(text string) (AppVersion, bool) {
	parts := strings.Split(text, ".")
	if len(parts) != len(AppVersion{}) {
		return AppVersion{}, false
	}
	var version AppVersion
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return AppVersion{}, false
		}
		version[index] = number
	}
	return version, true
}

func requireSupportedAppVersion(minimumVersion AppVersion, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appVersion, sentVersion := ParseAppVersion(r.Header.Get(appVersionHeader))
		if sentVersion && slices.Compare(appVersion[:], minimumVersion[:]) < 0 {
			writeError(w, r, errUpgradeRequired)
			return
		}
		next.ServeHTTP(w, r)
	})
}
