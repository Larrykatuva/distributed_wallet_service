package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/models"
)

type ProfileReqDto struct {
	ExternalId string             `json:"external_id" validate:"required"`
	Type       models.ProfileType `json:"type"        validate:"required,oneof=individual business"`
	FullName   string             `json:"full_name"   validate:"required"`
	Email      string             `json:"email"       validate:"required,email"`
	Phone      *string            `json:"phone"`
	Metadata   json.RawMessage    `json:"metadata"`
}

type ProfileResDto struct {
	ProfileReqDto

	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type WalletReqDto struct {
	ProfileID  uuid.UUID `json:"profile_id" validate:"required"`
	MerchantID uuid.UUID `json:"merchant_id" validate:"required"`
	Currency   string    `json:"currency" validate:"required"`
}

type ProfileDto struct {
	FullName string    `json:"full_name"`
	ID       uuid.UUID `json:"id"`
}

type WalletResDto struct {
	ID                uuid.UUID           `json:"id"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	Number            string              `json:"number"`
	Status            models.WalletStatus `json:"status"`
	Currency          string              `json:"currency"`
	AvailableBalance  float64             `json:"available_balance"`
	ProcessingBalance float64             `json:"processing_balance"`
	ActualBalance     float64             `json:"actual_balance"`
	Version           int64               `json:"version"`
	Profile           ProfileDto          `json:"profile"`
	Merchant          ProfileDto          `json:"merchant"`
}

type LedgerResDto struct {
	ID             string            `json:"id"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	WalletID       uuid.UUID         `json:"wallet_id"`
	TransactionID  string            `json:"transaction_id"`
	Type           models.LedgerType `json:"type"`
	Purpose        string            `json:"purpose"`
	Amount         float64           `json:"amount"`
	Currency       string            `json:"currency"`
	InitialBalance float64           `json:"initial_balance"`
	UpdatedBalance float64           `json:"updated_balance"`
}

type TransactionEntryDto struct {
	WalletID  uuid.UUID         `json:"wallet_id" validate:"required"`
	Action    models.LedgerType `json:"action"    validate:"required,oneof=debit credit"`
	Amount    float64           `json:"amount"    validate:"required,gt=0"`
	Purpose   string            `json:"purpose"   validate:"required"`
	IsFee     bool              `json:"is_fee"`
	IsInitial bool              `json:"is_initial"`
}

type TransactionReqDto struct {
	Type        models.TransactionType `json:"type"          validate:"required,oneof=transfer deposit withdrawal payment reversal adjustment"`
	OrderId     string                 `json:"order_id"      validate:"required"`
	ProviderRef string                 `json:"provider_ref"  validate:"required"`
	CallbackUrl string                 `json:"callback_url"  validate:"required,url"`
	TotalAmount float64                `json:"total_amount"  validate:"required,gt=0"`
	Fee         float64                `json:"fee"          validate:"required"`
	Currency    string                 `json:"currency"    validate:"required"`
	Purpose     *string                `json:"purpose"`
	Description *string                `json:"description"`
	Transfers   []TransactionEntryDto  `json:"transfers"     validate:"required,min=1,dive"`
}

type TransactionResDto struct {
	OrderID     *string                  `json:"order_id"`
	ProviderRef *string                  `json:"provider_ref"`
	Rrn         string                   `json:"rrn"`
	Type        models.TransactionType   `json:"type"`
	Status      models.TransactionStatus `json:"status"`
	Narration   string                   `json:"narration"`
}

type ErrMsgDto struct {
	Message string `json:"message"`
}
