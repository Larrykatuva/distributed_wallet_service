package grpc

import (
	"context"
	"encoding/json"

	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	"github.com/katuva/wallet/internal/validation"
	pb "github.com/katuva/wallet/proto/profile"
)

type ProfileGrpcHandler struct {
	pb.UnimplementedProfileServiceServer
	profiles *services.ProfileService
}

func NewProfileGrpcHandler(profiles *services.ProfileService) *ProfileGrpcHandler {
	return &ProfileGrpcHandler{profiles: profiles}
}

func (h *ProfileGrpcHandler) Register(_ context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	payload := types.ProfileReqDto{
		ExternalId: req.ExternalId,
		Type:       models.ProfileType(req.Type),
		FullName:   req.FullName,
		Email:      req.Email,
		Phone:      req.Phone,
	}
	if req.Metadata != nil && *req.Metadata != "" {
		if !json.Valid([]byte(*req.Metadata)) {
			return nil, toStatus(validation.FieldErrors{"metadata": "is not valid JSON"})
		}
		payload.Metadata = json.RawMessage(*req.Metadata)
	}
	if err := validation.Struct(&payload); err != nil {
		return nil, toStatus(err)
	}

	profile, err := h.profiles.Register(payload)
	if err != nil {
		return nil, toStatus(err)
	}

	return &pb.RegisterResponse{
		Id:         profile.ID.String(),
		ExternalId: profile.ExternalId,
		Type:       string(profile.Type),
		FullName:   profile.FullName,
		Email:      profile.Email,
	}, nil
}
