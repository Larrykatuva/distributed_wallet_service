package services

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/cache"
	"github.com/katuva/wallet/dpk/utils"
	"github.com/katuva/wallet/internal/actors"
	"github.com/katuva/wallet/internal/currency"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
	pb "github.com/katuva/wallet/proto/actors"
	"gorm.io/gorm"
)

// Cache TTLs. Completed transactions are immutable, so their rendered
// payload is cached; pending ones never are. order-used markers guard replays
// without a database round trip.
const (
	completedTxnTTL = 15 * time.Minute
	orderUsedTTL    = 48 * time.Hour
)

// lookbackWindow bounds created_at in point lookups so Postgres can prune
// partitions instead of scanning every month.
const lookbackWindow = 400 * 24 * time.Hour

type TransactionService struct {
	db       *gorm.DB
	wallets  *WalletService
	profiles *ProfileService
	cluster  *cluster.Cluster
	notifier actors.Notifier
}

func NewTransactionService(db *gorm.DB, c *cluster.Cluster, notifier actors.Notifier) *TransactionService {
	if notifier == nil {
		notifier = actors.NoopNotifier{}
	}
	return &TransactionService{db: db, wallets: NewWalletService(db), profiles: NewProfileService(db), cluster: c, notifier: notifier}
}

// Plan is the validated, minor-unit form of a transaction request.
type Plan struct {
	Currency    currency.Currency
	TotalAmount int64
	Fee         int64
	Legs        []actors.PlanEntry
}

// ValidateTransactionRequest enforces the rules struct tags cannot express and
// converts decimal amounts to minor units. wallets must contain every wallet
// referenced by payload; merchant is the profile named by merchant_id.
//
// Rules:
//   - currency is supported; every wallet holds exactly that currency
//     (no mixed-currency transfers, no implicit FX);
//   - amounts fit the currency's minor units (KES 100.005 is rejected);
//   - sum(debits) == sum(credits) == total_amount; fee <= total_amount
//     (the fee is part of the total, carried by its own legs);
//   - at least one debit is flagged is_initial;
//   - the merchant exists and owns at least one wallet in the transfers.
func ValidateTransactionRequest(payload types.TransactionReqDto, wallets map[uuid.UUID]models.Wallet, merchant *models.Profile) (Plan, error) {
	cur, ok := currency.Lookup(payload.Currency)
	if !ok {
		return Plan{}, invalid(fmt.Sprintf("currency %q is not a supported ISO 4217 code (see GET /v1/currencies)", payload.Currency))
	}
	if merchant == nil {
		return Plan{}, notFound(fmt.Sprintf("merchant %s not found", payload.MerchantID))
	}

	total, err := payload.TotalAmount.Minor(cur.Exponent)
	if err != nil {
		return Plan{}, invalid("total_amount: " + err.Error())
	}
	fee, err := payload.Fee.Minor(cur.Exponent)
	if err != nil {
		return Plan{}, invalid("fee: " + err.Error())
	}
	if total <= 0 {
		return Plan{}, invalid("total_amount must be greater than 0")
	}
	if fee < 0 || fee > total {
		return Plan{}, invalid(fmt.Sprintf("fee %s must be between 0 and total_amount %s", payload.Fee, payload.TotalAmount))
	}

	var debits, credits int64
	initialDebits := 0
	merchantOwnsWallet := false
	seen := map[string]bool{}
	legs := make([]actors.PlanEntry, 0, len(payload.Transfers))

	for i, tr := range payload.Transfers {
		amount, err := tr.Amount.Minor(cur.Exponent)
		if err != nil {
			return Plan{}, invalid(fmt.Sprintf("transfers[%d].amount: %v", i, err))
		}
		if amount <= 0 {
			return Plan{}, invalid(fmt.Sprintf("transfers[%d].amount must be greater than 0", i))
		}

		wl, ok := wallets[tr.WalletID]
		if !ok {
			return Plan{}, invalid(fmt.Sprintf("transfers[%d]: wallet %s not found", i, tr.WalletID))
		}
		seen[currency.Normalize(wl.Currency)] = true
		if wl.MerchantID != nil && *wl.MerchantID == merchant.ID {
			merchantOwnsWallet = true
		}

		switch tr.Action {
		case models.LedgerTypeDebit:
			debits += amount
			if tr.IsInitial {
				initialDebits++
			}
		case models.LedgerTypeCredit:
			credits += amount
		}

		legs = append(legs, actors.PlanEntry{
			WalletID: tr.WalletID, Action: tr.Action, Amount: amount, Purpose: tr.Purpose,
			IsFee: tr.IsFee, IsInitial: tr.IsInitial, IdempotencyKey: uuid.New(),
		})
	}

	if len(seen) > 1 {
		return Plan{}, invalid("transfers mix currencies (" + joinKeys(seen) + "); all wallets in a transaction must share one currency")
	}
	if !seen[cur.Code] {
		return Plan{}, invalid(fmt.Sprintf("transfers are in %s but the transaction currency is %s", joinKeys(seen), cur.Code))
	}
	if debits != credits {
		return Plan{}, invalid(fmt.Sprintf("transfers are unbalanced: debits %s != credits %s",
			types.MoneyFromMinor(debits, cur.Exponent), types.MoneyFromMinor(credits, cur.Exponent)))
	}
	if debits != total {
		return Plan{}, invalid(fmt.Sprintf("transfers total %s must equal total_amount %s (fee is included in the total)",
			types.MoneyFromMinor(debits, cur.Exponent), payload.TotalAmount))
	}
	if initialDebits == 0 {
		return Plan{}, invalid("at least one debit must be marked is_initial")
	}
	if !merchantOwnsWallet {
		return Plan{}, invalid(fmt.Sprintf("merchant %s owns none of the wallets in transfers", merchant.ID))
	}

	return Plan{Currency: cur, TotalAmount: total, Fee: fee, Legs: legs}, nil
}

func joinKeys(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// Initiate validates, persists the transaction as pending, and hands it to a
// TransactionActor. It returns as soon as the actor has the message; wallet
// processing completes asynchronously.
func (ts *TransactionService) Initiate(payload types.TransactionReqDto) (types.TransactionResDto, error) {
	ids := make([]uuid.UUID, 0, len(payload.Transfers))
	for _, tr := range payload.Transfers {
		ids = append(ids, tr.WalletID)
	}

	wallets, err := ts.wallets.ValidateWallets(ids)
	if err != nil {
		return types.TransactionResDto{}, err
	}
	merchant, err := ts.profiles.FindByID(payload.MerchantID)
	if err != nil {
		return types.TransactionResDto{}, err
	}
	plan, err := ValidateTransactionRequest(payload, wallets, merchant)
	if err != nil {
		return types.TransactionResDto{}, err
	}

	// order_id is the caller's idempotency handle: refuse a replay rather
	// than moving money twice. (Partitioning prevents a plain UNIQUE index.)
	// The cache marker answers the common case; the database is the authority.
	if cache.Exists(cache.OrderUsedKey(payload.OrderId)) {
		if existing, err := ts.findByOrderID(payload.OrderId); err == nil && existing != nil {
			return types.TransactionFromModel(*existing), conflict("order_id already used")
		}
	}
	if existing, err := ts.findByOrderID(payload.OrderId); err != nil {
		return types.TransactionResDto{}, err
	} else if existing != nil {
		return types.TransactionFromModel(*existing), conflict("order_id already used")
	}

	transaction := ts.buildTransaction(payload, plan, wallets)

	// The plan (legs + idempotency keys) is stored with the row so a lost
	// orchestrator can be recovered from the ledger.
	if transaction.Transfers, err = actors.EncodePlan(plan.Legs); err != nil {
		return types.TransactionResDto{}, err
	}

	// Resolve the grain before persisting so a cluster outage fails fast
	// without leaving an orphaned pending row.
	txnPID := ts.cluster.Get(transaction.ID.String(), actors.KindTransaction)
	if txnPID == nil {
		return types.TransactionResDto{}, unavailable("transaction processor unavailable")
	}

	if err = ts.db.Create(&transaction).Error; err != nil {
		return types.TransactionResDto{}, fmt.Errorf("create transaction: %w", err)
	}
	cache.Set(cache.OrderUsedKey(payload.OrderId), true, orderUsedTTL)

	ts.cluster.ActorSystem.Root.Send(txnPID, &pb.StartTransactionMsg{
		TransactionId: transaction.ID.String(),
		CreatedAtUnix: transaction.CreatedAt.Add(-time.Minute).Unix(),
		Transfers:     actors.PlanToProto(plan.Legs),
	})

	return types.TransactionFromModel(transaction), nil
}

func (ts *TransactionService) buildTransaction(payload types.TransactionReqDto, plan Plan, wallets map[uuid.UUID]models.Wallet) models.Transaction {
	transaction := models.Transaction{
		ID:          uuid.New(),
		MerchantID:  &payload.MerchantID,
		Amount:      plan.TotalAmount,
		Currency:    plan.Currency.Code,
		Fee:         plan.Fee,
		Type:        payload.Type,
		Status:      models.TransactionStatusPending,
		Purpose:     payload.Purpose,
		RRN:         utils.GenerateRrn(),
		OrderID:     &payload.OrderId,
		ProviderRef: &payload.ProviderRef,
		CallbackURL: &payload.CallbackUrl,
		Description: payload.Description,
		Narration:   "Transaction accepted for processing",
	}

	for _, tr := range payload.Transfers {
		if !tr.IsInitial {
			continue
		}
		wl := wallets[tr.WalletID]
		if tr.Action == models.LedgerTypeDebit && transaction.WalletFrom == nil {
			transaction.WalletFrom = &wl.ID
			transaction.ProfileFrom = &wl.ProfileID
			transaction.SenderName = &wl.Profile.FullName
			transaction.AccountFrom = &wl.Number
			if wl.Merchant != nil {
				transaction.MerchantFrom = &wl.Merchant.ID
				transaction.SenderMerchant = &wl.Merchant.FullName
			}
		}
		if tr.Action == models.LedgerTypeCredit && transaction.WalletTo == nil {
			transaction.WalletTo = &wl.ID
			transaction.ProfileTo = &wl.ProfileID
			transaction.ReceiverName = &wl.Profile.FullName
			transaction.AccountTo = &wl.Number
			if wl.Merchant != nil {
				transaction.MerchantTo = &wl.Merchant.ID
				transaction.ReceiverMerchant = &wl.Merchant.FullName
			}
		}
	}
	return transaction
}

// GetByRRN looks a transaction up by its retrieval reference number.
func (ts *TransactionService) GetByRRN(rrn string) (types.TransactionResDto, error) {
	return ts.detail(cache.TransactionRRNKey(rrn), func() (*models.Transaction, error) {
		var tx models.Transaction
		err := ts.db.Where("rrn = ? AND created_at >= ?", rrn, time.Now().Add(-lookbackWindow)).First(&tx).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, notFound("transaction not found")
		}
		if err != nil {
			return nil, fmt.Errorf("get transaction: %w", err)
		}
		return &tx, nil
	})
}

// GetByID looks a transaction up by its UUID.
func (ts *TransactionService) GetByID(id uuid.UUID) (types.TransactionResDto, error) {
	return ts.detail(cache.TransactionKey(id.String()), func() (*models.Transaction, error) { return ts.findByID(id) })
}

// detail renders a single transaction with transfers and balances. Completed
// transactions are immutable, so the rendered payload is cached under every
// lookup key; pending ones are always read fresh.
func (ts *TransactionService) detail(key string, load func() (*models.Transaction, error)) (types.TransactionResDto, error) {
	var cached types.TransactionResDto
	if cache.Get(key, &cached) {
		return cached, nil
	}
	tx, err := load()
	if err != nil {
		return types.TransactionResDto{}, err
	}
	dto := TransactionPayload(ts.db)(*tx, nil).WithTransfers(*tx)
	if tx.IsCompleted {
		cache.Set(cache.TransactionKey(tx.ID.String()), dto, completedTxnTTL)
		cache.Set(cache.TransactionRRNKey(tx.RRN), dto, completedTxnTTL)
		if tx.OrderID != nil {
			cache.Set(cache.TransactionOrderKey(*tx.OrderID), dto, completedTxnTTL)
		}
	}
	return dto, nil
}

func (ts *TransactionService) findByID(id uuid.UUID) (*models.Transaction, error) {
	var tx models.Transaction
	err := ts.db.Where("id = ? AND created_at >= ?", id, time.Now().Add(-lookbackWindow)).First(&tx).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, notFound("transaction not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get transaction: %w", err)
	}
	return &tx, nil
}

// ResendCallback re-delivers the completion webhook for a finished
// transaction. Balances are rebuilt from the ledger.
func (ts *TransactionService) ResendCallback(rrn string) error {
	var tx models.Transaction
	err := ts.db.Where("rrn = ? AND created_at >= ?", rrn, time.Now().Add(-lookbackWindow)).First(&tx).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return notFound("transaction not found")
	}
	if err != nil {
		return fmt.Errorf("get transaction: %w", err)
	}
	if !tx.IsCompleted {
		return invalid("transaction is still processing; callback is sent on completion")
	}
	if tx.CallbackURL == nil || *tx.CallbackURL == "" {
		return invalid("transaction has no callback_url")
	}
	ts.notifier.Notify(tx, nil)
	return nil
}

// List returns transactions newest first within the filter window.
// From/To bound created_at so Postgres prunes partitions.
func (ts *TransactionService) List(f types.TransactionFilter, page types.Pagination) (types.Page[types.TransactionResDto], error) {
	page = page.Normalize()
	if f.To.IsZero() {
		f.To = time.Now()
	}
	if f.From.IsZero() {
		f.From = f.To.Add(-30 * 24 * time.Hour)
	}
	if f.From.After(f.To) {
		return types.Page[types.TransactionResDto]{}, invalid("from must be before to")
	}

	q := ts.db.Model(&models.Transaction{}).Where("created_at >= ? AND created_at <= ?", f.From, f.To)
	if f.MerchantID != nil {
		q = q.Where("merchant_id = ?", *f.MerchantID)
	}
	if f.ProfileID != nil {
		q = q.Where("profile_from = ? OR profile_to = ?", *f.ProfileID, *f.ProfileID)
	}
	if f.WalletID != nil {
		q = q.Where("wallet_from = ? OR wallet_to = ?", *f.WalletID, *f.WalletID)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.Type != nil {
		q = q.Where("type = ?", *f.Type)
	}
	if f.OrderID != nil {
		q = q.Where("order_id = ?", *f.OrderID)
	}
	if f.ProviderRef != nil {
		q = q.Where("provider_ref = ?", *f.ProviderRef)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return types.Page[types.TransactionResDto]{}, fmt.Errorf("count transactions: %w", err)
	}
	var rows []models.Transaction
	err := q.Order("created_at DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&rows).Error
	if err != nil {
		return types.Page[types.TransactionResDto]{}, fmt.Errorf("list transactions: %w", err)
	}
	out := make([]types.TransactionResDto, 0, len(rows))
	for _, r := range rows {
		out = append(out, types.TransactionFromModel(r)) // lean: no balances/transfers in listings
	}
	return types.NewPage(out, page, total), nil
}

// GetByOrderID looks a transaction up by the caller's order reference.
func (ts *TransactionService) GetByOrderID(orderID string) (types.TransactionResDto, error) {
	return ts.detail(cache.TransactionOrderKey(orderID), func() (*models.Transaction, error) {
		tx, err := ts.findByOrderID(orderID)
		if err != nil {
			return nil, err
		}
		if tx == nil {
			return nil, notFound("transaction not found")
		}
		return tx, nil
	})
}

func (ts *TransactionService) findByOrderID(orderID string) (*models.Transaction, error) {
	var tx models.Transaction
	err := ts.db.Where("order_id = ? AND created_at >= ?", orderID, time.Now().Add(-lookbackWindow)).
		Order("created_at DESC").First(&tx).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find transaction: %w", err)
	}
	return &tx, nil
}
