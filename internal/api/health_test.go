package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeDatabase struct {
	pingError error
}

func (database fakeDatabase) Ping(ctx context.Context) error {
	return database.pingError
}

func TestHealthAnswersPongWhenDatabaseIsReachable(t *testing.T) {
	response := httptest.NewRecorder()
	NewRouter(fakeDatabase{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := strings.TrimSpace(response.Body.String()); body != `{"message":"pong"}` {
		t.Fatalf("body = %s, want {\"message\":\"pong\"}", body)
	}
}

func TestHealthFailsWhenDatabaseIsUnreachable(t *testing.T) {
	response := httptest.NewRecorder()
	unreachableDatabase := fakeDatabase{pingError: errors.New("connection refused")}
	NewRouter(unreachableDatabase).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}
