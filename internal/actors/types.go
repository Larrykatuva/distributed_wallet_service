package actors

import (
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/models"
)

type ErrorMsg string

const (
	InsufficientFundsMsg ErrorMsg = "insufficient funds"
	InactiveWalletMsg    ErrorMsg = "inactive wallet"
	WalletNotFoundMsg    ErrorMsg = "wallet not found"
)

type TransferEntry struct {
	WalletID       uuid.UUID
	Action         models.LedgerType
	Amount         float64
	Purpose        string
	Success        bool
	Error          error
	IdempotencyKey uuid.UUID
	Done           bool
}

type ExecuteTransaction struct {
	TransactionID uuid.UUID
	Transfers     []TransferEntry
}

type AddTransactionStatusMsg struct {
}

type AuditTransactionMsg struct {
	TransactionID uuid.UUID
}

type StartTransactionMsg struct {
	TransactionID uuid.UUID
	Transfers     []TransferEntry
	Date          time.Time
}

type CreditWalletMsg struct {
	WalletID                      uuid.UUID
	Amount                        float64
	Purpose                       string
	Action                        models.LedgerType
	TransactionID, IdempotencyKey uuid.UUID
}

type DebitWalletMsg struct {
	WalletID                      uuid.UUID
	Amount                        float64
	Purpose                       string
	Action                        models.LedgerType
	TransactionID, IdempotencyKey uuid.UUID
}

type WalletResultMsg struct {
	TransactionID, WalletID, IdempotencyKey uuid.UUID
	InitialBalance, UpdatedBalance          float64
	Amount                                  float64
	Error                                   error
}
