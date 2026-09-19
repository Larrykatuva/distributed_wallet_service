package http

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/types"
	"github.com/katuva/wallet/internal/validation"
)

// pagination reads page and page_size query params.
func pagination(r *http.Request) types.Pagination {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	return types.Pagination{Page: page, PageSize: size}.Normalize()
}

// queryString returns a pointer to a non-empty query param, else nil.
func queryString(r *http.Request, key string) *string {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return nil
	}
	return &v
}

// queryUUID parses an optional UUID query param, recording bad input in fe.
func queryUUID(r *http.Request, key string, fe validation.FieldErrors) *uuid.UUID {
	v := queryString(r, key)
	if v == nil {
		return nil
	}
	id, err := uuid.Parse(*v)
	if err != nil {
		fe[key] = "is not a valid UUID"
		return nil
	}
	return &id
}

// queryTime parses an optional RFC3339 or YYYY-MM-DD query param.
func queryTime(r *http.Request, key string, fe validation.FieldErrors) time.Time {
	v := queryString(r, key)
	if v == nil {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, *v); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", *v); err == nil {
		return t
	}
	fe[key] = "must be RFC3339 or YYYY-MM-DD"
	return time.Time{}
}
