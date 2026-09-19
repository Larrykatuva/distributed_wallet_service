package models

import "github.com/google/uuid"

// WalletMovement is how one transaction moved one wallet, as observed by the
// TransactionActor from wallet replies: no ledger read required.
type WalletMovement struct {
	WalletID       uuid.UUID
	Currency       string
	InitialBalance int64 // minor units, before the first movement
	UpdatedBalance int64 // minor units, after the last movement (reversals included)
	Entries        int   // wallet operations applied for this transaction
}
