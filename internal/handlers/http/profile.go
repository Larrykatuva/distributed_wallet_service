package http

import (
	"net/http"

	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

type ProfileHandler struct {
	db             *gorm.DB
	profileService *services.ProfileServiceImpl
}

func NewProfileHandler(db *gorm.DB) *ProfileHandler {
	return &ProfileHandler{
		db:             db,
		profileService: services.NewProfileService(db),
	}
}

func (p *ProfileHandler) Register(w http.ResponseWriter, r *http.Request) {
	var payload types.ProfileReqDto

	if !Validate(r, w, &payload) {
		return
	}

	profile, err := p.profileService.Register(payload)
	if err != nil {
		WriteError(w, http.StatusBadRequest, types.ErrMsgDto{Message: err.Error()})
		return
	}

	WriteSuccess(w, http.StatusCreated, profile)
}
