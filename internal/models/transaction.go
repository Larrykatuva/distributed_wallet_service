package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type TransactionType string
type TransactionStatus string

const (
	TransactionTypeTransfer   TransactionType = "transfer"
	TransactionTypeDeposit    TransactionType = "deposit"
	TransactionTypeWithdrawal TransactionType = "withdrawal"
	TransactionTypePayment    TransactionType = "payment"
	TransactionTypeReversal   TransactionType = "reversal"
	TransactionTypeAdjustment TransactionType = "adjustment"
)

const (
	TransactionStatusPending    TransactionStatus = "pending"
	TransactionStatusProcessing TransactionStatus = "processing"
	TransactionStatusSuccess    TransactionStatus = "success"
	TransactionStatusFailed     TransactionStatus = "failed"
)

// Transaction records a financial event. Amounts are int64 minor units.
// The table is range-partitioned by created_at, so every lookup should
// include a created_at bound to allow partition pruning.
type Transaction struct {
	ID uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`

	// Parties
	MerchantID   *uuid.UUID `gorm:"type:uuid;not null;index"` // merchant initiating the transaction
	MerchantFrom *uuid.UUID `gorm:"type:uuid;index"`
	MerchantTo   *uuid.UUID `gorm:"type:uuid;index"`
	ProfileFrom  *uuid.UUID `gorm:"type:uuid;index"`
	ProfileTo    *uuid.UUID `gorm:"type:uuid;index"`

	// Display names
	SenderName       *string `gorm:"type:text"`
	ReceiverName     *string `gorm:"type:text"`
	SenderMerchant   *string `gorm:"type:text"`
	ReceiverMerchant *string `gorm:"type:text"`
	AccountFrom      *string `gorm:"type:text"`
	AccountTo        *string `gorm:"type:text"`

	// Wallets
	WalletFrom *uuid.UUID `gorm:"type:uuid;index"`
	WalletTo   *uuid.UUID `gorm:"type:uuid;index"`

	// Financials
	Amount   int64  `gorm:"not null;check:amount > 0"`
	Currency string `gorm:"type:text;not null;default:KES"`
	Fee      int64  `gorm:"not null;default:0;check:fee >= 0"`

	// Classification
	Type    TransactionType   `gorm:"type:transaction_type;not null"`
	Status  TransactionStatus `gorm:"type:transaction_status;not null;default:pending"`
	Purpose *string           `gorm:"type:text"`

	// References
	RRN         string  `gorm:"type:text;not null"`
	OrderID     *string `gorm:"type:text;index"`
	ProviderRef *string `gorm:"type:text;index"`
	CallbackURL *string `gorm:"type:text"`

	// Transfer plan (see actors.PlanEntry): legs with their idempotency keys,
	// used to recover a transaction whose orchestrator was lost.
	Transfers json.RawMessage `gorm:"type:jsonb;not null;default:'[]'"`

	// Description
	Narration   string  `gorm:"type:text"`
	Description *string `gorm:"type:text"`

	// Completion
	IsCompleted   bool       `gorm:"not null;default:false"`
	DateCompleted *time.Time `gorm:"type:timestamptz"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (Transaction) TableName() string {
	return "transactions"
}
