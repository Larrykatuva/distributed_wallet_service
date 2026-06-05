package models

import (
	"time"

	"github.com/google/uuid"
)

type WalletStatus string

const (
	WalletStatusActive    WalletStatus = "active"
	WalletStatusSuspended WalletStatus = "suspended"
	WalletStatusFrozen    WalletStatus = "frozen"
	WalletStatusClosed    WalletStatus = "closed"
)

type Wallet struct {
	ID                uuid.UUID    `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Number            string       `gorm:"type:text;uniqueIndex;not null"`
	ProfileID         uuid.UUID    `gorm:"type:uuid;not null;index"`
	MerchantID        *uuid.UUID   `gorm:"type:uuid;index"`
	Status            WalletStatus `gorm:"type:wallet_status;not null;default:active"`
	Currency          string       `gorm:"type:text;not null;default:KES;index"`
	AvailableBalance  float64      `gorm:"not null;default:0;check:available_balance >= 0"`
	ProcessingBalance float64      `gorm:"not null;default:0;check:processing_balance >= 0"`
	ActualBalance     float64      `gorm:"not null;default:0;check:actual_balance >= 0"`
	Checksum          string       `gorm:"type:text;not null;default:''"`
	Version           int64        `gorm:"not null;default:0"`
	CreatedAt         time.Time    `gorm:"not null"`
	UpdatedAt         time.Time    `gorm:"not null"`

	// Associations
	Profile  Profile  `gorm:"foreignKey:ProfileID"`
	Merchant *Profile `gorm:"foreignKey:MerchantID"`
}

func (Wallet) TableName() string {
	return "wallets"
}
