package routers

import (
	"github.com/go-chi/chi/v5"
	"github.com/katuva/wallet/internal/handlers/http"
	"github.com/katuva/wallet/internal/services"
)

// Services groups the service layer shared by every transport.
type Services struct {
	Profiles     *services.ProfileService
	Wallets      *services.WalletService
	Transactions *services.TransactionService
}

// Router mounts the versioned API.
func Router(svc Services) *chi.Mux {
	r := chi.NewRouter()

	profiles := http.NewProfileHandler(svc.Profiles)
	wallets := http.NewWalletHandler(svc.Wallets)
	transactions := http.NewTransactionHandler(svc.Transactions)

	r.Get("/currencies", http.ListCurrencies)

	r.Route("/profile", func(pr chi.Router) {
		pr.Post("/register", profiles.Register)
		pr.Get("/", profiles.List)
		pr.Get("/username/{username}", profiles.GetByUsername)
		pr.Get("/{id}", profiles.Get)
	})

	r.Route("/wallet", func(wr chi.Router) {
		wr.Post("/create", wallets.Create)
		wr.Get("/", wallets.List)
		wr.Get("/number/{number}", wallets.GetByNumber)
		wr.Get("/profile/{profileId}", wallets.ListByProfile)
		wr.Get("/{id}", wallets.Get)
	})

	r.Route("/transaction", func(tr chi.Router) {
		tr.Post("/initiate", transactions.Initiate)
		tr.Get("/", transactions.List)
		tr.Get("/rrn/{rrn}", transactions.GetByRRN)
		tr.Post("/rrn/{rrn}/resend-callback", transactions.ResendCallback)
		tr.Get("/order/{orderId}", transactions.GetByOrderID)
		tr.Get("/{id}", transactions.GetByID)
	})

	return r
}
