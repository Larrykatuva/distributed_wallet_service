package services

import "errors"

// Sentinel errors let transports pick the right status code.
var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrInvalid       = errors.New("invalid request")
	ErrUnavailable   = errors.New("service unavailable")
	ErrInternalState = errors.New("internal error")
)

// ServiceError pairs a sentinel kind with a caller-facing message.
type ServiceError struct {
	Kind    error
	Message string
}

func (e *ServiceError) Error() string { return e.Message }
func (e *ServiceError) Unwrap() error { return e.Kind }

func notFound(msg string) error    { return &ServiceError{Kind: ErrNotFound, Message: msg} }
func conflict(msg string) error    { return &ServiceError{Kind: ErrConflict, Message: msg} }
func invalid(msg string) error     { return &ServiceError{Kind: ErrInvalid, Message: msg} }
func unavailable(msg string) error { return &ServiceError{Kind: ErrUnavailable, Message: msg} }
