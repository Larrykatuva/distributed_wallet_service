package models

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// TransferLeg is one leg of a transaction as persisted in
// transactions.transfers. It is the full request leg plus the idempotency
// keys the orchestrator uses, so the row alone is enough to audit or recover
// the transaction.
type TransferLeg struct {
	WalletID       uuid.UUID  `json:"wallet_id"`
	Action         LedgerType `json:"action"`
	Amount         int64      `json:"amount"` // minor units
	Purpose        string     `json:"purpose"`
	IsFee          bool       `json:"is_fee"`
	IsInitial      bool       `json:"is_initial"`
	IdempotencyKey uuid.UUID  `json:"idempotency_key"`
	ReversalKey    *uuid.UUID `json:"reversal_key,omitempty"`
}

// EncodeTransferLegs serialises legs for the transactions.transfers column.
func EncodeTransferLegs(legs []TransferLeg) (json.RawMessage, error) {
	b, err := json.Marshal(legs)
	if err != nil {
		return nil, fmt.Errorf("encode transfers: %w", err)
	}
	return b, nil
}

// DecodeTransferLegs parses the transactions.transfers column.
func DecodeTransferLegs(raw json.RawMessage) ([]TransferLeg, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var legs []TransferLeg
	if err := json.Unmarshal(raw, &legs); err != nil {
		return nil, fmt.Errorf("decode transfers: %w", err)
	}
	return legs, nil
}
