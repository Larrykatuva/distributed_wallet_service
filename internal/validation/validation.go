// Package validation holds the single validator instance shared by the HTTP
// and gRPC handlers so both transports enforce identical DTO rules.
package validation

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/katuva/wallet/internal/currency"
	"github.com/katuva/wallet/internal/types"
)

var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Money validates through its float view so gt/gte tags apply.
	v.RegisterCustomTypeFunc(func(field reflect.Value) any {
		if m, ok := field.Interface().(types.Money); ok {
			if !m.IsSet() {
				return nil // triggers `required`
			}
			return m.Float()
		}
		return nil
	}, types.Money{})
	// `currency`: an ISO 4217 code from the supported registry (case-insensitive).
	_ = v.RegisterValidation("currency", func(fl validator.FieldLevel) bool {
		return currency.Supported(fl.Field().String())
	})
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return field.Name
		}
		return name
	})
	return v
}

// FieldErrors maps a JSON field path to a human-readable message.
type FieldErrors map[string]string

func (f FieldErrors) Error() string {
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" "+f[k])
	}
	return strings.Join(parts, "; ")
}

// Struct validates dst and returns FieldErrors on failure, or nil.
func Struct(dst any) error {
	err := validate.Struct(dst)
	if err == nil {
		return nil
	}

	var errs validator.ValidationErrors
	if !errors.As(err, &errs) {
		return err
	}

	out := make(FieldErrors, len(errs))
	for _, e := range errs {
		out[fieldPath(e)] = message(e)
	}
	return out
}

// fieldPath strips the root struct name and returns e.g. "transfers[0].amount".
func fieldPath(e validator.FieldError) string {
	ns := e.Namespace()
	if i := strings.Index(ns, "."); i >= 0 {
		return ns[i+1:]
	}
	return e.Field()
}

func message(e validator.FieldError) string {
	switch e.Tag() {
	case "required":
		return "is required"
	case "oneof":
		return "must be one of: " + strings.ReplaceAll(e.Param(), " ", ", ")
	case "gt":
		return "must be greater than " + e.Param()
	case "gte":
		return "must be at least " + e.Param()
	case "min":
		return fmt.Sprintf("must have at least %s entries", e.Param())
	case "len":
		return fmt.Sprintf("must be exactly %s characters", e.Param())
	case "url":
		return "is not a valid URL"
	case "currency":
		return "is not a supported ISO 4217 currency code (see GET /v1/currencies)"
	case "email":
		return "is not a valid email"
	default:
		return "is invalid (" + e.Tag() + ")"
	}
}
