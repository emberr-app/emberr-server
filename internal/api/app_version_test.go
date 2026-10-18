package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOldAppVersionGetsUpgradeRequired(t *testing.T) {
	router := NewRouter(Services{Database: fakeDatabase{}, MinimumAppVersion: AppVersion{1, 2, 0}})
	request := httptest.NewRequest(http.MethodGet, paramsPath+"?email=ana@example.com", nil)
	request.Header.Set(appVersionHeader, "1.1.9")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	requireErrorCode(t, response, http.StatusUpgradeRequired, "UPGRADE_REQUIRED")
}
