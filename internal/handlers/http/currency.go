package http

import (
	"net/http"

	"github.com/katuva/wallet/internal/currency"
)

// ListCurrencies returns every currency wallets can be created in.
func ListCurrencies(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"count":      len(currency.Codes()),
		"currencies": currency.All(),
	})
}
