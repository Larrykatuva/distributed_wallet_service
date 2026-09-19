package models

import (
	"time"

	"github.com/google/uuid"
)

type LedgerType string

const (
	LedgerTypeDebit  LedgerType = "debit"
	LedgerTypeCredit LedgerType = "credit"
)

// Opposite returns the ledger type that undoes this one.
func (t LedgerType) Opposite() LedgerType {
	if t == LedgerTypeDebit {
		return LedgerTypeCredit
	}
	return LedgerTypeDebit
}

// Ledger is the immutable, append-only record of every balance change.
// Amounts are int64 minor units. IdempotencyKey lets the WalletActor detect
// a redelivered message and skip re-applying it.
type Ledger struct {
	ID             int64      `gorm:"primaryKey;autoIncrement"`
	WalletID       uuid.UUID  `gorm:"type:uuid;not null;index"`
	TransactionID  uuid.UUID  `gorm:"type:uuid;not null;index"`
	IdempotencyKey *uuid.UUID `gorm:"type:uuid;index"`
	Type           LedgerType `gorm:"type:ledger_type;not null"`
	Purpose        string     `gorm:"type:text"`
	Amount         int64      `gorm:"not null;check:amount > 0"`
	Currency       string     `gorm:"type:text;not null;default:KES"`
	InitialBalance int64      `gorm:"not null"`
	UpdatedBalance int64      `gorm:"not null"`
	CreatedAt      time.Time  `gorm:"not null"`
}

func (Ledger) TableName() string {
	return "ledgers"
}
