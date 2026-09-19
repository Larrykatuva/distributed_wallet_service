package http

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/validation"
)

// decode parses and validates the JSON body, writing the error response itself.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, map[string]string{"body": err.Error()})
		return false
	}
	if err := validation.Struct(dst); err != nil {
		var fe validation.FieldErrors
		if errors.As(err, &fe) {
			writeError(w, http.StatusUnprocessableEntity, fe)
		} else {
			writeError(w, http.StatusBadRequest, map[string]string{"body": err.Error()})
		}
		return false
	}
	return true
}

// writeServiceError maps service sentinel errors to HTTP status codes.
func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	msg := "internal server error"

	switch {
	case errors.Is(err, services.ErrNotFound):
		status, msg = http.StatusNotFound, err.Error()
	case errors.Is(err, services.ErrConflict):
		status, msg = http.StatusConflict, err.Error()
	case errors.Is(err, services.ErrInvalid):
		status, msg = http.StatusBadRequest, err.Error()
	case errors.Is(err, services.ErrUnavailable):
		status, msg = http.StatusServiceUnavailable, err.Error()
	default:
		logger.ErrorLog.Printf("http: %v", err)
	}
	writeError(w, status, map[string]string{"message": msg})
}

func writeError(w http.ResponseWriter, code int, body any) {
	writeJSON(w, code, map[string]any{"status": code, "errors": body})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.ErrorLog.Printf("http: encode response: %v", err)
	}
}
