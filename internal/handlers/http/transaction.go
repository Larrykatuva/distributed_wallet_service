package http

import (
	"net/http"

	"github.com/asynkron/protoactor-go/cluster"
	"github.com/katuva/wallet/internal/services"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

type Transaction struct {
	db                 *gorm.DB
	transactionService *services.TransactionServiceImpl
}

func NewTransactionHandler(db *gorm.DB, cluster *cluster.Cluster) *Transaction {
	return &Transaction{
		db:                 db,
		transactionService: services.NewTransactionService(db, cluster),
	}
}

func (t *Transaction) Initiate(w http.ResponseWriter, r *http.Request) {
	var payload types.TransactionReqDto

	if !Validate(r, w, &payload) {
		return
	}

	result, err := t.transactionService.Initiate(payload)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	WriteSuccess(w, 200, result)
}
