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

type TransactionHandler struct {
	transactions *services.TransactionService
}

func NewTransactionHandler(transactions *services.TransactionService) *TransactionHandler {
	return &TransactionHandler{transactions: transactions}
}

// Initiate accepts the transaction and returns 202: processing is async.
func (h *TransactionHandler) Initiate(w http.ResponseWriter, r *http.Request) {
	var payload types.TransactionReqDto
	if !decode(w, r, &payload) {
		return
	}

	result, err := h.transactions.Initiate(payload)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

// List: GET /transaction?merchant_id=&profile_id=&wallet_id=&status=&type=&order_id=&provider_ref=&from=&to=&page=&page_size=
func (h *TransactionHandler) List(w http.ResponseWriter, r *http.Request) {
	fe := validation.FieldErrors{}
	f := types.TransactionFilter{
		MerchantID:  queryUUID(r, "merchant_id", fe),
		ProfileID:   queryUUID(r, "profile_id", fe),
		WalletID:    queryUUID(r, "wallet_id", fe),
		OrderID:     queryString(r, "order_id"),
		ProviderRef: queryString(r, "provider_ref"),
		From:        queryTime(r, "from", fe),
		To:          queryTime(r, "to", fe),
	}
	if st := queryString(r, "status"); st != nil {
		s := models.TransactionStatus(*st)
		switch s {
		case models.TransactionStatusPending, models.TransactionStatusProcessing, models.TransactionStatusSuccess, models.TransactionStatusFailed:
			f.Status = &s
		default:
			fe["status"] = "must be one of: pending, processing, success, failed"
		}
	}
	if tp := queryString(r, "type"); tp != nil {
		t := models.TransactionType(*tp)
		f.Type = &t
	}
	if len(fe) > 0 {
		writeError(w, http.StatusBadRequest, fe)
		return
	}

	page, err := h.transactions.List(f, pagination(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *TransactionHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, map[string]string{"id": "is not a valid UUID"})
		return
	}
	result, err := h.transactions.GetByID(id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *TransactionHandler) GetByRRN(w http.ResponseWriter, r *http.Request) {
	result, err := h.transactions.GetByRRN(chi.URLParam(r, "rrn"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *TransactionHandler) GetByOrderID(w http.ResponseWriter, r *http.Request) {
	result, err := h.transactions.GetByOrderID(chi.URLParam(r, "orderId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ResendCallback: POST /transaction/rrn/{rrn}/resend-callback
func (h *TransactionHandler) ResendCallback(w http.ResponseWriter, r *http.Request) {
	if err := h.transactions.ResendCallback(chi.URLParam(r, "rrn")); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "callback queued for delivery"})
}
