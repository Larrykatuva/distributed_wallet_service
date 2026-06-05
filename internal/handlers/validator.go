package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/go-playground/validator/v10"
	"github.com/katuva/wallet/dpk/logger"
)

var validate = validator.New()

func init() {
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := field.Tag.Get("json")
		if name == "" || name == "-" {
			return field.Name
		}
		return name
	})
}

func Validate(r *http.Request, w http.ResponseWriter, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		WriteError(w, http.StatusBadRequest, map[string]string{
			"message": err.Error(),
		})
		return false
	}

	if err := validate.Struct(dst); err != nil {
		var errs validator.ValidationErrors
		errors.As(err, &errs)
		WriteError(w, http.StatusUnprocessableEntity, formatErrors(errs))
		return false
	}

	return true
}

func formatErrors(errs validator.ValidationErrors) map[string]string {
	messages := map[string]string{
		"required": "is required",
		"oneof":    "is invalid",
		"gt":       "must be greater than 0",
		"min":      "must have at least one entry",
		"url":      "is not a valid URL",
	}

	out := make(map[string]string, len(errs))
	for _, e := range errs {
		msg, ok := messages[e.Tag()]
		if !ok {
			msg = e.Tag()
		}
		out[e.Field()] = msg
	}
	return out
}

func WriteError(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	err := json.NewEncoder(w).Encode(map[string]any{
		"status": code,
		"errors": body,
	})
	if err != nil {
		logger.ErrorLog.Println(err)
	}
}

func WriteSuccess(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	err := json.NewEncoder(w).Encode(body)
	if err != nil {
		logger.ErrorLog.Println(err)
	}
}
