package services

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/katuva/wallet/dpk/cache"
	"github.com/katuva/wallet/dpk/utils"
	"github.com/katuva/wallet/internal/actors"
	"github.com/katuva/wallet/internal/currency"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
	pb "github.com/katuva/wallet/proto/actors"
	"gorm.io/gorm"
)

// completedTxnTTL: completed transactions are immutable, so their rendered
// payload is cached; pending ones never are.
const completedTxnTTL = 15 * time.Minute

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

	return ts.dispatch(ts.buildTransaction(payload, plan, wallets), plan.Legs)
}

// dispatch persists a validated transaction as pending with its plan and
// hands it to a TransactionActor.
func (ts *TransactionService) dispatch(transaction models.Transaction, legs []actors.PlanEntry) (types.TransactionResDto, error) {
	var err error

	// The plan (legs + idempotency keys) is stored with the row so a lost
	// orchestrator can be recovered from the ledger.
	if transaction.Transfers, err = actors.EncodePlan(legs); err != nil {
		return types.TransactionResDto{}, err
	}

	// Resolve the grain before persisting so a cluster outage fails fast
	// without leaving an orphaned pending row.
	txnPID := ts.cluster.Get(transaction.ID.String(), actors.KindTransaction)
	if txnPID == nil {
		return types.TransactionResDto{}, unavailable("transaction processor unavailable")
	}

	if err = ts.create(&transaction); err != nil {
		return types.TransactionResDto{}, fmt.Errorf("create transaction: %w", err)
	}

	ts.cluster.ActorSystem.Root.Send(txnPID, &pb.StartTransactionMsg{
		TransactionId: transaction.ID.String(),
		CreatedAtUnix: transaction.CreatedAt.Add(-time.Minute).Unix(),
		Transfers:     actors.PlanToProto(legs),
	})

	return types.TransactionFromModel(transaction), nil
}

// create inserts the transaction, drawing a new RRN if the one it has collides.
func (ts *TransactionService) create(transaction *models.Transaction) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = ts.db.Create(transaction).Error; err == nil || !isRRNConflict(err) {
			return err
		}
		transaction.RRN = utils.GenerateRrn()
	}
	return err
}

func isRRNConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "rrn")
}

// Reverse undoes a successful transaction. The reversal is a new transaction
// of type reversal whose legs are the original's ledger rows with debit and
// credit swapped; it runs through the TransactionActor like any other, and on
// success the actor marks the original reversed. The reversal carries the
// original's order_id, and its provider_ref is the original RRN.
//
// Rules:
//   - only a successful, non-reversal transaction can be reversed;
//   - one live reversal per transaction (a failed one may be retried);
//   - the ledger must match the persisted plan exactly, leg for leg;
//   - every wallet is still active and holds the available balance its
//     reversal debits need, so the reversal does not fail half way.
func (ts *TransactionService) Reverse(payload types.TransactionReverseReqDto) (types.TransactionResDto, error) {
	from, to, err := dayWindow("transaction_date", payload.TransactionDate)
	if err != nil {
		return types.TransactionResDto{}, err
	}

	var orig models.Transaction
	err = ts.db.Where("rrn = ? AND created_at >= ? AND created_at < ?", payload.RRN, from, to).First(&orig).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.TransactionResDto{}, notFound("transaction not found on " + payload.TransactionDate)
	}
	if err != nil {
		return types.TransactionResDto{}, fmt.Errorf("get transaction: %w", err)
	}

	if orig.Status != models.TransactionStatusSuccess {
		return types.TransactionResDto{}, invalid(fmt.Sprintf("only successful transactions can be reversed; this one is %s", orig.Status))
	}
	if orig.Type == models.TransactionTypeReversal {
		return types.TransactionResDto{}, invalid("a reversal cannot itself be reversed")
	}
	if orig.MerchantID == nil || orig.OrderID == nil {
		return types.TransactionResDto{}, invalid("transaction has no merchant or order_id; cannot reverse")
	}
	cur, ok := currency.Lookup(orig.Currency)
	if !ok {
		return types.TransactionResDto{}, invalid(fmt.Sprintf("transaction currency %q is not supported", orig.Currency))
	}

	// A reversal names its original by RRN in provider_ref and is always
	// created after it, so the replay check skips every older partition.
	var existing models.Transaction
	err = ts.db.Where("provider_ref = ? AND type = ? AND created_at >= ? AND created_at < ?",
		orig.RRN, models.TransactionTypeReversal, orig.CreatedAt, notAfter()).
		Order("created_at DESC").Limit(1).Find(&existing).Error
	if err != nil {
		return types.TransactionResDto{}, fmt.Errorf("find reversal: %w", err)
	}
	if existing.ID != uuid.Nil && existing.Status != models.TransactionStatusFailed {
		return types.TransactionFromModel(existing), conflict(fmt.Sprintf("transaction already has a reversal (rrn %s, %s)", existing.RRN, existing.Status))
	}

	legs, err := ts.reversalLegs(orig)
	if err != nil {
		return types.TransactionResDto{}, err
	}

	ids := make([]uuid.UUID, 0, len(legs))
	for _, l := range legs {
		ids = append(ids, l.WalletID)
	}
	wallets, err := ts.wallets.ValidateWallets(ids)
	if err != nil {
		return types.TransactionResDto{}, err
	}
	if err = ts.ensureReversalFunds(legs, cur); err != nil {
		return types.TransactionResDto{}, err
	}

	purpose := "reversal of " + orig.RRN
	callbackURL := ""
	if orig.CallbackURL != nil {
		callbackURL = *orig.CallbackURL
	}
	if payload.CallbackUrl != nil && *payload.CallbackUrl != "" {
		callbackURL = *payload.CallbackUrl
	}
	entries := make([]types.TransactionEntryDto, 0, len(legs))
	for _, l := range legs {
		entries = append(entries, types.TransactionEntryDto{WalletID: l.WalletID, Action: l.Action,
			Amount: types.MoneyFromMinor(l.Amount, cur.Exponent), Purpose: l.Purpose, IsFee: l.IsFee, IsInitial: l.IsInitial})
	}
	req := types.TransactionReqDto{
		Type:        models.TransactionTypeReversal,
		MerchantID:  *orig.MerchantID,
		OrderId:     *orig.OrderID,
		ProviderRef: orig.RRN,
		CallbackUrl: callbackURL,
		Purpose:     &purpose,
		Description: payload.Description,
		Transfers:   entries,
	}

	transaction := ts.buildTransaction(req, Plan{Currency: cur, TotalAmount: orig.Amount, Fee: orig.Fee, Legs: legs}, wallets)
	transaction.Narration = "Reversal accepted for processing"
	return ts.dispatch(transaction, legs)
}

// reversalLegs inverts the original's ledger rows; each reversal leg keeps
// the row's purpose, prefixed "Reversal: ", so its ledger entry reads as the
// undo of that specific movement. Each row must match a
// planned leg by idempotency key (wallet, action and amount included), and
// every leg must have exactly one row: anything else means the transaction
// needs reconciliation, not an automatic reversal.
func (ts *TransactionService) reversalLegs(orig models.Transaction) ([]actors.PlanEntry, error) {
	plan, err := actors.DecodePlan(orig.Transfers)
	if err != nil {
		return nil, err
	}
	var rows []models.Ledger
	err = ts.db.Where("transaction_id = ? AND created_at >= ? AND created_at < ?", orig.ID, orig.CreatedAt.Add(-time.Minute), notAfter()).
		Order("id ASC").Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load ledger: %w", err)
	}

	mismatch := invalid("transaction ledger does not match its transfers; reconcile it before reversing")
	if len(rows) == 0 || len(rows) != len(plan) {
		return nil, mismatch
	}
	byKey := make(map[uuid.UUID]actors.PlanEntry, len(plan))
	for _, l := range plan {
		byKey[l.IdempotencyKey] = l
	}

	legs := make([]actors.PlanEntry, 0, len(rows))
	for _, r := range rows {
		if r.IdempotencyKey == nil {
			return nil, mismatch
		}
		leg, ok := byKey[*r.IdempotencyKey]
		if !ok || leg.WalletID != r.WalletID || leg.Action != r.Type || leg.Amount != r.Amount {
			return nil, mismatch
		}
		delete(byKey, *r.IdempotencyKey)

		legs = append(legs, actors.PlanEntry{
			WalletID: r.WalletID, Action: r.Type.Opposite(), Amount: r.Amount, Purpose: "Reversal: " + r.Purpose,
			IsFee: leg.IsFee, IsInitial: leg.IsInitial, IdempotencyKey: uuid.New(),
		})
	}
	return legs, nil
}

// ensureReversalFunds checks, before anything moves, that each wallet's
// available balance covers the sum of its reversal debits. Credits are not
// netted: every debit runs before any credit. Balances
// are read fresh from the database, never from cache. The WalletActor still
// enforces this per leg; the check only keeps a doomed reversal from starting.
func (ts *TransactionService) ensureReversalFunds(legs []actors.PlanEntry, cur currency.Currency) error {
	need := map[uuid.UUID]int64{}
	for _, l := range legs {
		if l.Action == models.LedgerTypeDebit {
			need[l.WalletID] += l.Amount
		}
	}
	if len(need) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(need))
	for id := range need {
		ids = append(ids, id)
	}

	var wallets []models.Wallet
	if err := ts.db.Select("id", "number", "available_balance").Where("id IN ?", ids).Find(&wallets).Error; err != nil {
		return fmt.Errorf("load wallet balances: %w", err)
	}
	if len(wallets) != len(ids) {
		return notFound("a wallet in the transaction no longer exists")
	}
	for _, wl := range wallets {
		if wl.AvailableBalance < need[wl.ID] {
			return invalid(fmt.Sprintf("wallet %s has insufficient available balance to reverse: needs %s, has %s", wl.Number,
				types.MoneyFromMinor(need[wl.ID], cur.Exponent), types.MoneyFromMinor(wl.AvailableBalance, cur.Exponent)))
		}
	}
	return nil
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

// dayWindow bounds created_at to the caller's date, padded a day each side
// for time zones, so Postgres reads one monthly partition (two at a boundary).
func dayWindow(field, date string) (from, to time.Time, err error) {
	day, err := time.Parse("2006-01-02", date)
	if err != nil {
		return from, to, invalid(field + " must be YYYY-MM-DD")
	}
	return day.AddDate(0, 0, -1), day.AddDate(0, 0, 2), nil
}

// notAfter bounds rows that cannot be dated in the future, pruning the
// months created ahead of time.
func notAfter() time.Time { return time.Now().Add(24 * time.Hour) }

// GetByRRN looks a transaction up by its retrieval reference number and
// creation date (YYYY-MM-DD).
func (ts *TransactionService) GetByRRN(rrn, date string) (types.TransactionResDto, error) {
	from, to, err := dayWindow("date", date)
	if err != nil {
		return types.TransactionResDto{}, err
	}
	return ts.detail(cache.TransactionRRNKey(rrn), from, to, func() (*models.Transaction, error) {
		return ts.findByRRN(rrn, from, to)
	})
}

// GetByID looks a transaction up by its UUID and creation date (YYYY-MM-DD).
func (ts *TransactionService) GetByID(id uuid.UUID, date string) (types.TransactionResDto, error) {
	from, to, err := dayWindow("date", date)
	if err != nil {
		return types.TransactionResDto{}, err
	}
	return ts.detail(cache.TransactionKey(id.String()), from, to, func() (*models.Transaction, error) {
		var tx models.Transaction
		err := ts.db.Where("id = ? AND created_at >= ? AND created_at < ?", id, from, to).First(&tx).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, notFound("transaction not found")
		}
		if err != nil {
			return nil, fmt.Errorf("get transaction: %w", err)
		}
		return &tx, nil
	})
}

// detail renders a single transaction with transfers and balances. Completed
// transactions are immutable, so the rendered payload is cached under every
// lookup key; pending ones are always read fresh. A cached copy is only used
// inside the requested date window, so a wrong date is not found either way.
func (ts *TransactionService) detail(key string, from, to time.Time, load func() (*models.Transaction, error)) (types.TransactionResDto, error) {
	var cached types.TransactionResDto
	if cache.Get(key, &cached) && !cached.CreatedAt.Before(from) && cached.CreatedAt.Before(to) {
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
		// A reversal shares its original's order_id; that key stays the original's.
		if tx.OrderID != nil && tx.Type != models.TransactionTypeReversal {
			cache.Set(cache.TransactionOrderKey(*tx.OrderID), dto, completedTxnTTL)
		}
	}
	return dto, nil
}

func (ts *TransactionService) findByRRN(rrn string, from, to time.Time) (*models.Transaction, error) {
	var tx models.Transaction
	err := ts.db.Where("rrn = ? AND created_at >= ? AND created_at < ?", rrn, from, to).First(&tx).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, notFound("transaction not found")
	}
	if err != nil {
		return nil, fmt.Errorf("get transaction: %w", err)
	}
	return &tx, nil
}

// ResendCallback re-delivers the completion webhook for a finished
// transaction, found by RRN and creation date. Balances are rebuilt from the ledger.
func (ts *TransactionService) ResendCallback(rrn, date string) error {
	from, to, err := dayWindow("date", date)
	if err != nil {
		return err
	}
	tx, err := ts.findByRRN(rrn, from, to)
	if err != nil {
		return err
	}
	if !tx.IsCompleted {
		return invalid("transaction is still processing; callback is sent on completion")
	}
	if tx.CallbackURL == nil || *tx.CallbackURL == "" {
		return invalid("transaction has no callback_url")
	}
	ts.notifier.Notify(*tx, nil)
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

// GetByOrderID looks a transaction up by the caller's order reference and
// creation date (YYYY-MM-DD).
func (ts *TransactionService) GetByOrderID(orderID, date string) (types.TransactionResDto, error) {
	from, to, err := dayWindow("date", date)
	if err != nil {
		return types.TransactionResDto{}, err
	}
	return ts.detail(cache.TransactionOrderKey(orderID), from, to, func() (*models.Transaction, error) {
		tx, err := ts.findByOrderID(orderID, from, to)
		if err != nil {
			return nil, err
		}
		if tx == nil {
			return nil, notFound("transaction not found")
		}
		return tx, nil
	})
}

func (ts *TransactionService) findByOrderID(orderID string, from, to time.Time) (*models.Transaction, error) {
	var tx models.Transaction
	// Reversals reuse their original's order_id; the order belongs to the original.
	err := ts.db.Where("order_id = ? AND type <> ? AND created_at >= ? AND created_at < ?", orderID, models.TransactionTypeReversal, from, to).
		Order("created_at DESC").First(&tx).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find transaction: %w", err)
	}
	return &tx, nil
}
