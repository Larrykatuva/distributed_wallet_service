package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	"github.com/katuva/wallet/internal/validation"
)

type ProfileHandler struct {
	profiles *services.ProfileService
}

func NewProfileHandler(profiles *services.ProfileService) *ProfileHandler {
	return &ProfileHandler{profiles: profiles}
}

func (h *ProfileHandler) Register(w http.ResponseWriter, r *http.Request) {
	var payload types.ProfileReqDto
	if !decode(w, r, &payload) {
		return
	}

	profile, err := h.profiles.Register(payload)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, profile)
}

// List: GET /profile?external_id=&type=&search=&page=&page_size=
func (h *ProfileHandler) List(w http.ResponseWriter, r *http.Request) {
	fe := validation.FieldErrors{}
	f := types.ProfileFilter{ExternalID: queryString(r, "external_id"), Search: queryString(r, "search")}
	if t := queryString(r, "type"); t != nil {
		pt := models.ProfileType(*t)
		if pt != models.ProfileTypeIndividual && pt != models.ProfileTypeBusiness {
			fe["type"] = "must be one of: individual, business"
		}
		f.Type = &pt
	}
	if len(fe) > 0 {
		writeError(w, http.StatusBadRequest, fe)
		return
	}

	page, err := h.profiles.List(f, pagination(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *ProfileHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, map[string]string{"id": "is not a valid UUID"})
		return
	}
	profile, err := h.profiles.Get(id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

// GetByUsername: GET /profile/username/{username} where username is an email or phone.
func (h *ProfileHandler) GetByUsername(w http.ResponseWriter, r *http.Request) {
	profile, err := h.profiles.GetByUsername(chi.URLParam(r, "username"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}
