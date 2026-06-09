package grpc

import (
	"context"
	"encoding/json" // ← add

	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	pb "github.com/katuva/wallet/proto/profile" // ← add
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type ProfileGrpcHandler struct {
	pb.UnimplementedProfileServiceServer
	profileService *services.ProfileServiceImpl
}

func NewProfileGrpcHandler(db *gorm.DB) *ProfileGrpcHandler {
	return &ProfileGrpcHandler{
		profileService: services.NewProfileService(db),
	}
}

func (h *ProfileGrpcHandler) Register(
	ctx context.Context,
	req *pb.RegisterRequest,
) (*pb.RegisterResponse, error) {

	// Validate
	if req.ExternalId == "" {
		return nil, status.Error(codes.InvalidArgument, "external_id is required")
	}
	if req.Type == "" {
		return nil, status.Error(codes.InvalidArgument, "type is required")
	}
	profileType := models.ProfileType(req.Type)
	if profileType != models.ProfileTypeIndividual && profileType != models.ProfileTypeBusiness {
		return nil, status.Error(codes.InvalidArgument, "type must be one of: individual, business")
	}
	if req.FullName == "" {
		return nil, status.Error(codes.InvalidArgument, "full_name is required")
	}
	if req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}

	// Build DTO
	payload := types.ProfileReqDto{
		ExternalId: req.ExternalId,
		Type:       profileType,
		FullName:   req.FullName,
		Email:      req.Email,
	}
	if req.Phone != nil {
		payload.Phone = req.Phone
	}
	if req.Metadata != nil {
		payload.Metadata = json.RawMessage(*req.Metadata)
	}

	// Call service
	profile, err := h.profileService.Register(payload)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	return &pb.RegisterResponse{
		Id:         profile.ID.String(),
		ExternalId: profile.ExternalId,
		Type:       string(profile.Type),
		FullName:   profile.FullName,
		Email:      profile.Email,
	}, nil
}
