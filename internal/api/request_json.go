package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

const maxRequestBodyBytes = 4 << 20

func readJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(destination); err != nil {
		var bodyTooLargeError *http.MaxBytesError
		if errors.As(err, &bodyTooLargeError) {
			return err
		}
		return newBadRequestError("request body is not valid JSON")
	}
	return nil
}
