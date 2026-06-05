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

type Ledger struct {
	ID             int64      `gorm:"primaryKey;autoIncrement"`
	WalletID       uuid.UUID  `gorm:"type:uuid;not null;index"`
	TransactionID  uuid.UUID  `gorm:"type:uuid;not null;index"`
	Type           LedgerType `gorm:"type:ledger_type;not null"`
	Purpose        string     `gorm:"type:text"`
	Amount         float64    `gorm:"not null;check:amount > 0"`
	Currency       string     `gorm:"type:text;not null;default:KES"`
	InitialBalance float64    `gorm:"not null"`
	UpdatedBalance float64    `gorm:"not null"`
	CreatedAt      time.Time  `gorm:"not null"`

	// Associations
	Wallet      Wallet      `gorm:"foreignKey:WalletID"`
	Transaction Transaction `gorm:"foreignKey:TransactionID"`
}

func (Ledger) TableName() string {
	return "ledgers"
}
