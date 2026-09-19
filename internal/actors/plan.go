package actors

import (
	"encoding/json"

	"github.com/katuva/wallet/internal/models"
	pb "github.com/katuva/wallet/proto/actors"
)

// PlanEntry is the persisted form of a transaction leg (models.TransferLeg).
type PlanEntry = models.TransferLeg

// EncodePlan serialises legs for the transactions.transfers column.
func EncodePlan(plan []PlanEntry) (json.RawMessage, error) { return models.EncodeTransferLegs(plan) }

// DecodePlan parses the transactions.transfers column.
func DecodePlan(raw json.RawMessage) ([]PlanEntry, error) { return models.DecodeTransferLegs(raw) }

// PlanToProto converts a plan into StartTransactionMsg transfer entries.
func PlanToProto(plan []PlanEntry) []*pb.TransferEntry {
	out := make([]*pb.TransferEntry, 0, len(plan))
	for _, e := range plan {
		pe := &pb.TransferEntry{
			WalletId:       e.WalletID.String(),
			Action:         string(e.Action),
			Amount:         e.Amount,
			Purpose:        e.Purpose,
			IdempotencyKey: e.IdempotencyKey.String(),
			IsFee:          e.IsFee,
			IsInitial:      e.IsInitial,
		}
		if e.ReversalKey != nil {
			pe.ReversalKey = e.ReversalKey.String()
		}
		out = append(out, pe)
	}
	return out
}
