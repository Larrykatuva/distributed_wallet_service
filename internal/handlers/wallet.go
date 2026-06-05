package handlers

import (
	"net/http"

	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

type WalletHandler struct {
	db            *gorm.DB
	wallerService *services.WalletServiceImpl
}

func NewWalletHandler(db *gorm.DB) *WalletHandler {
	return &WalletHandler{
		db:            db,
		wallerService: services.NewWalletService(db),
	}
}

func (ws *WalletHandler) Create(w http.ResponseWriter, r *http.Request) {
	var payload types.WalletReqDto

	if !Validate(r, w, &payload) {
		return
	}

	wallet, err := ws.wallerService.Create(payload)
	if err != nil {
		WriteError(w, http.StatusBadRequest, types.ErrMsgDto{Message: err.Error()})
		return
	}

	WriteSuccess(w, http.StatusOK, wallet)
}
