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

type WalletHandler struct {
	wallets *services.WalletService
}

func NewWalletHandler(wallets *services.WalletService) *WalletHandler {
	return &WalletHandler{wallets: wallets}
}

func (h *WalletHandler) Create(w http.ResponseWriter, r *http.Request) {
	var payload types.WalletReqDto
	if !decode(w, r, &payload) {
		return
	}

	wallet, err := h.wallets.Create(payload)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, wallet)
}

func (h *WalletHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, map[string]string{"id": "is not a valid UUID"})
		return
	}

	wallet, err := h.wallets.Get(id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wallet)
}

// GetByNumber: GET /wallet/number/{number}
func (h *WalletHandler) GetByNumber(w http.ResponseWriter, r *http.Request) {
	wallet, err := h.wallets.GetByNumber(chi.URLParam(r, "number"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wallet)
}

// List: GET /wallet?merchant_id=&profile_id=&currency=&status=&page=&page_size=
func (h *WalletHandler) List(w http.ResponseWriter, r *http.Request) {
	fe := validation.FieldErrors{}
	f := types.WalletFilter{
		MerchantID: queryUUID(r, "merchant_id", fe),
		ProfileID:  queryUUID(r, "profile_id", fe),
		Currency:   queryString(r, "currency"),
	}
	if st := queryString(r, "status"); st != nil {
		ws := models.WalletStatus(*st)
		switch ws {
		case models.WalletStatusActive, models.WalletStatusSuspended, models.WalletStatusFrozen, models.WalletStatusClosed:
			f.Status = &ws
		default:
			fe["status"] = "must be one of: active, suspended, frozen, closed"
		}
	}
	if len(fe) > 0 {
		writeError(w, http.StatusBadRequest, fe)
		return
	}
	h.list(w, r, f)
}

// ListByProfile: GET /wallet/profile/{profileId}?merchant_id=&currency=&status=
func (h *WalletHandler) ListByProfile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "profileId"))
	if err != nil {
		writeError(w, http.StatusBadRequest, map[string]string{"profileId": "is not a valid UUID"})
		return
	}
	fe := validation.FieldErrors{}
	f := types.WalletFilter{
		ProfileID:  &id,
		MerchantID: queryUUID(r, "merchant_id", fe),
		Currency:   queryString(r, "currency"),
	}
	if st := queryString(r, "status"); st != nil {
		ws := models.WalletStatus(*st)
		switch ws {
		case models.WalletStatusActive, models.WalletStatusSuspended, models.WalletStatusFrozen, models.WalletStatusClosed:
			f.Status = &ws
		default:
			fe["status"] = "must be one of: active, suspended, frozen, closed"
		}
	}
	if len(fe) > 0 {
		writeError(w, http.StatusBadRequest, fe)
		return
	}
	h.list(w, r, f)
}

func (h *WalletHandler) list(w http.ResponseWriter, r *http.Request, f types.WalletFilter) {
	page, err := h.wallets.List(f, pagination(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
