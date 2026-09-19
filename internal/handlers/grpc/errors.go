package grpc

import (
	"errors"

	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/validation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// toStatus maps validation and service errors onto gRPC status codes.
func toStatus(err error) error {
	var fe validation.FieldErrors
	switch {
	case errors.As(err, &fe):
		return status.Error(codes.InvalidArgument, fe.Error())
	case errors.Is(err, services.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, services.ErrConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, services.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, services.ErrUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	default:
		logger.ErrorLog.Printf("grpc: %v", err)
		return status.Error(codes.Internal, "internal error")
	}
}
