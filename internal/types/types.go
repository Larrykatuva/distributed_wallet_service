package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/currency"
	"github.com/katuva/wallet/internal/models"
)

// Monetary fields on the API are decimal major units (Money, e.g. 110.00).
// They are converted to int64 minor units using the currency exponent.

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
	ProfileID  uuid.UUID `json:"profile_id"  validate:"required"`
	MerchantID uuid.UUID `json:"merchant_id" validate:"required"`
	Currency   string    `json:"currency"    validate:"required,currency"`
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
	AvailableBalance  Money               `json:"available_balance"`
	ProcessingBalance Money               `json:"processing_balance"`
	ActualBalance     Money               `json:"actual_balance"`
	Version           int64               `json:"version"`
	Profile           ProfileDto          `json:"profile"`
	Merchant          ProfileDto          `json:"merchant"`
}

type TransactionEntryDto struct {
	WalletID  uuid.UUID         `json:"wallet_id" validate:"required"`
	Action    models.LedgerType `json:"action"    validate:"required,oneof=debit credit"`
	Amount    Money             `json:"amount"    validate:"required,gt=0"`
	Purpose   string            `json:"purpose"   validate:"required"`
	IsFee     bool              `json:"is_fee"`
	IsInitial bool              `json:"is_initial"`
}

// TransactionReqDto describes a balanced set of transfers.
// Business rules (debits == credits == total_amount, fee <= total_amount,
// one currency, merchant owns a wallet involved) are enforced in
// services.ValidateTransactionRequest.
type TransactionReqDto struct {
	Type        models.TransactionType `json:"type"          validate:"required,oneof=transfer deposit withdrawal payment reversal adjustment"`
	MerchantID  uuid.UUID              `json:"merchant_id"   validate:"required"`
	OrderId     string                 `json:"order_id"      validate:"required"`
	ProviderRef string                 `json:"provider_ref"  validate:"required"`
	CallbackUrl string                 `json:"callback_url"  validate:"required,url"`
	TotalAmount Money                  `json:"total_amount"  validate:"required,gt=0"`
	Fee         Money                  `json:"fee"           validate:"gte=0"`
	Currency    string                 `json:"currency"      validate:"required,currency"`
	Purpose     *string                `json:"purpose"`
	Description *string                `json:"description"`
	Transfers   []TransactionEntryDto  `json:"transfers"     validate:"required,min=2,dive"`
}

// TransactionLegDto is a transfer leg as returned with a transaction.
type TransactionLegDto struct {
	WalletID  uuid.UUID         `json:"wallet_id"`
	Action    models.LedgerType `json:"action"`
	Amount    Money             `json:"amount"`
	Purpose   string            `json:"purpose"`
	IsFee     bool              `json:"is_fee"`
	IsInitial bool              `json:"is_initial"`
}

// WalletBalanceDto reports how a transaction moved one wallet: the balance
// before its first ledger entry and after its last (reversals included).
type WalletBalanceDto struct {
	WalletID       uuid.UUID `json:"wallet_id"`
	Currency       string    `json:"currency"`
	InitialBalance Money     `json:"initial_balance"`
	UpdatedBalance Money     `json:"updated_balance"`
	Entries        int       `json:"entries"` // ledger rows this transaction wrote for the wallet
}

type TransactionResDto struct {
	ID            uuid.UUID                `json:"id"`
	MerchantID    *uuid.UUID               `json:"merchant_id"`
	OrderID       *string                  `json:"order_id"`
	ProviderRef   *string                  `json:"provider_ref"`
	Rrn           string                   `json:"rrn"`
	CallbackURL   *string                  `json:"callback_url"`
	Type          models.TransactionType   `json:"type"`
	Status        models.TransactionStatus `json:"status"`
	Narration     string                   `json:"narration"`
	Amount        Money                    `json:"amount"`
	Fee           Money                    `json:"fee"`
	Currency      string                   `json:"currency"`
	IsCompleted   bool                     `json:"is_completed"`
	DateCompleted *time.Time               `json:"date_completed"`
	CreatedAt     time.Time                `json:"created_at"`
	Transfers     []TransactionLegDto      `json:"transfers,omitempty"` // only on GET by id/rrn/order
	Balances      []WalletBalanceDto       `json:"balances"`
}

// TransactionFromModel maps a persisted transaction to its API shape,
// rendering minor units as decimals for the transaction currency.
func TransactionFromModel(t models.Transaction) TransactionResDto {
	exp := 2
	if c, ok := currency.Lookup(t.Currency); ok {
		exp = c.Exponent
	}
	return TransactionResDto{
		ID:            t.ID,
		Balances:      []WalletBalanceDto{},
		MerchantID:    t.MerchantID,
		OrderID:       t.OrderID,
		ProviderRef:   t.ProviderRef,
		Rrn:           t.RRN,
		CallbackURL:   t.CallbackURL,
		Type:          t.Type,
		Status:        t.Status,
		Narration:     t.Narration,
		Amount:        MoneyFromMinor(t.Amount, exp),
		Fee:           MoneyFromMinor(t.Fee, exp),
		Currency:      t.Currency,
		IsCompleted:   t.IsCompleted,
		DateCompleted: t.DateCompleted,
		CreatedAt:     t.CreatedAt,
	}
}

// WithTransfers attaches the persisted legs to a transaction response. Used
// by single-transaction reads only; initiate responses, listings and the
// callback stay lean.
func (d TransactionResDto) WithTransfers(t models.Transaction) TransactionResDto {
	exp := 2
	if c, ok := currency.Lookup(t.Currency); ok {
		exp = c.Exponent
	}
	legs, _ := models.DecodeTransferLegs(t.Transfers)
	d.Transfers = make([]TransactionLegDto, 0, len(legs))
	for _, l := range legs {
		d.Transfers = append(d.Transfers, TransactionLegDto{
			WalletID: l.WalletID, Action: l.Action, Amount: MoneyFromMinor(l.Amount, exp),
			Purpose: l.Purpose, IsFee: l.IsFee, IsInitial: l.IsInitial,
		})
	}
	return d
}

// TransactionFilter narrows a transaction listing.
type TransactionFilter struct {
	MerchantID  *uuid.UUID
	ProfileID   *uuid.UUID // matches profile_from or profile_to
	WalletID    *uuid.UUID // matches wallet_from or wallet_to
	Status      *models.TransactionStatus
	Type        *models.TransactionType
	OrderID     *string
	ProviderRef *string
	From, To    time.Time // created_at window; required for partition pruning
}

// ProfileFilter narrows a profile listing.
type ProfileFilter struct {
	ExternalID *string
	Type       *models.ProfileType
	Search     *string // case-insensitive match on full_name, email, phone
}

// WalletFilter narrows a wallet listing.
type WalletFilter struct {
	MerchantID *uuid.UUID
	ProfileID  *uuid.UUID
	Currency   *string
	Status     *models.WalletStatus
}

type ErrMsgDto struct {
	Message string `json:"message"`
}
