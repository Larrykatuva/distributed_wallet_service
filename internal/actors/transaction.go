package actors

import (
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/models"
	pb "github.com/katuva/wallet/proto/actors"
	"gorm.io/gorm"
)

// stage is the TransactionActor lifecycle. Transitions only move forward:
//
//	idle → processing → (all applied) → finalized
//	                  → (any failed)  → reversing → finalized
type stage int

const (
	stageIdle stage = iota
	stageProcessing
	stageReversing
	stageFinalized
)

// DefaultReplyTimeout bounds how long the orchestrator waits for wallet
// replies in each stage before treating the silent legs as failed.
const DefaultReplyTimeout = 30 * time.Second

const finalizeAttempts = 3

// transfer is the actor's private bookkeeping for one leg of the transaction.
type transfer struct {
	walletID  uuid.UUID
	action    models.LedgerType
	amount    int64
	purpose   string
	isFee     bool
	isInitial bool

	// key is the idempotency key of the original operation; reversalKey the
	// key of its compensating operation once one has been issued.
	key         uuid.UUID
	reversalKey *uuid.UUID

	done    bool // reply received for the in-flight op
	applied bool // original op moved the balance

	failCode ErrorCode
	failMsg  string

	reversed    bool
	reversalErr string

	// Balances reported by the wallet, and the order replies arrived in.
	// A wallet's replies to one sender arrive in the order it processed them.
	initial, updated   int64
	rInitial, rUpdated int64
	seq, rSeq          int
}

// inFlightKey is the key a reply must carry to match this leg right now.
func (tr *transfer) inFlightKey(s stage) string {
	if s == stageReversing && tr.reversalKey != nil {
		return tr.reversalKey.String()
	}
	return tr.key.String()
}

// Notifier is told about every finalized transaction (e.g. to fire a webhook).
// movements are the per-wallet balances the orchestrator observed; nil means
// the caller has none (recovery) and the notifier derives them from the ledger.
type Notifier interface {
	Notify(tx models.Transaction, movements []models.WalletMovement)
}

// NoopNotifier satisfies Notifier and does nothing.
type NoopNotifier struct{}

func (NoopNotifier) Notify(models.Transaction, []models.WalletMovement) {}

// TransactionActor orchestrates one transaction: it fans debits and credits out
// to the owning WalletActors, collects replies, reverses applied legs if any
// leg failed, writes the final status, notifies, and then stops itself.
//
// It can also resume a transaction whose previous orchestrator was lost: with
// StartTransactionMsg.resume it reconstructs progress from the ledger rows
// that carry the plan's idempotency keys and continues from there.
type TransactionActor struct {
	db           *gorm.DB
	resolver     WalletResolver
	notifier     Notifier
	replyTimeout time.Duration

	transactionID uuid.UUID
	createdAt     time.Time // lower bound for partition pruning
	transfers     []transfer
	stage         stage
	timer         *time.Timer
	replies       int // arrival counter for ordering wallet replies
}

// NewTransactionActor is the cluster kind producer. Transaction data arrives
// with StartTransactionMsg.
func NewTransactionActor(db *gorm.DB, resolver WalletResolver, notifier Notifier) actor.Actor {
	if notifier == nil {
		notifier = NoopNotifier{}
	}
	return &TransactionActor{db: db, resolver: resolver, notifier: notifier, replyTimeout: DefaultReplyTimeout}
}

// WithReplyTimeout overrides the per-stage wait for wallet replies.
func (t *TransactionActor) WithReplyTimeout(d time.Duration) *TransactionActor {
	if d > 0 {
		t.replyTimeout = d
	}
	return t
}

func (t *TransactionActor) Receive(ctx actor.Context) {
	switch msg := ctx.Message().(type) {
	case *cluster.ClusterInit:
		if id, err := uuid.Parse(msg.Identity.Identity); err == nil {
			t.transactionID = id
		}

	case *pb.StartTransactionMsg:
		t.start(ctx, msg)

	case *pb.WalletResultMsg:
		t.collect(ctx, msg)

	case *pb.TransactionTimeoutMsg:
		t.timeout(ctx, stage(msg.Stage))

	case *pb.FinalizeTransactionMsg:
		t.finalize(ctx)

	case *actor.Stopping:
		t.stopTimer()
	}
}

func (t *TransactionActor) start(ctx actor.Context, msg *pb.StartTransactionMsg) {
	if t.stage != stageIdle {
		logger.WarningLog.Printf("txn %s: duplicate StartTransactionMsg ignored (stage %d)", msg.TransactionId, t.stage)
		return
	}

	id, err := uuid.Parse(msg.TransactionId)
	if err != nil {
		logger.ErrorLog.Printf("txn start: bad transaction_id %q: %v", msg.TransactionId, err)
		ctx.Poison(ctx.Self())
		return
	}
	if t.transactionID != uuid.Nil && t.transactionID != id {
		logger.ErrorLog.Printf("txn %s: StartTransactionMsg for %s delivered to wrong grain", t.transactionID, id)
		return
	}
	t.transactionID = id
	t.createdAt = time.Unix(msg.CreatedAtUnix, 0).UTC()

	if !t.loadTransfers(msg.Transfers) {
		t.stage = stageProcessing
		t.failAll(CodeInvalidMessage, "malformed transfer entry")
		t.finalize(ctx)
		return
	}

	// pending → processing (or processing → processing on resume). Zero rows
	// means the row is gone or already terminal: never run it twice.
	allowed := []models.TransactionStatus{models.TransactionStatusPending}
	if msg.Resume {
		allowed = append(allowed, models.TransactionStatusProcessing)
	}
	res := t.db.Model(&models.Transaction{}).
		Where("id = ? AND created_at >= ? AND status IN ?", t.transactionID, t.createdAt, allowed).
		Updates(map[string]any{"status": models.TransactionStatusProcessing, "updated_at": time.Now()})
	if res.Error != nil {
		logger.ErrorLog.Printf("txn %s: could not mark processing: %v", t.transactionID, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		logger.WarningLog.Printf("txn %s: not startable (resume=%v), ignored", t.transactionID, msg.Resume)
		ctx.Poison(ctx.Self())
		return
	}

	if msg.Resume {
		t.resume(ctx)
		return
	}

	t.stage = stageProcessing
	t.fireTransfers(ctx)
	t.armTimeout(ctx)
	t.advance(ctx)
}

func (t *TransactionActor) loadTransfers(entries []*pb.TransferEntry) bool {
	t.transfers = make([]transfer, 0, len(entries))
	for _, e := range entries {
		wid, err1 := uuid.Parse(e.WalletId)
		key, err2 := uuid.Parse(e.IdempotencyKey)
		if err1 != nil || err2 != nil || e.Amount <= 0 {
			logger.ErrorLog.Printf("txn %s: malformed transfer entry %+v", t.transactionID, e)
			return false
		}
		tr := transfer{walletID: wid, action: models.LedgerType(e.Action), amount: e.Amount, purpose: e.Purpose,
			isFee: e.IsFee, isInitial: e.IsInitial, key: key}
		if e.ReversalKey != "" {
			if rk, err := uuid.Parse(e.ReversalKey); err == nil {
				tr.reversalKey = &rk
			}
		}
		t.transfers = append(t.transfers, tr)
	}
	return true
}

// resume rebuilds progress from the ledger. A leg is applied if a ledger row
// carries its key, reversed if one carries its reversal key. Legs already
// settled are not re-sent; the rest continue under their original keys, which
// the WalletActor treats idempotently if a late message did land after all.
func (t *TransactionActor) resume(ctx actor.Context) {
	var keys []uuid.UUID
	err := t.db.Model(&models.Ledger{}).
		Where("transaction_id = ? AND created_at >= ?", t.transactionID, t.createdAt).
		Pluck("idempotency_key", &keys).Error
	if err != nil {
		logger.ErrorLog.Printf("txn %s: resume could not read ledger: %v", t.transactionID, err)
		return
	}
	seen := make(map[uuid.UUID]bool, len(keys))
	for _, k := range keys {
		seen[k] = true
	}

	reversing := false
	for i := range t.transfers {
		tr := &t.transfers[i]
		tr.applied = seen[tr.key]
		if tr.reversalKey != nil {
			reversing = true
			tr.reversed = seen[*tr.reversalKey]
		}
	}

	if reversing {
		// The lost orchestrator had already decided to fail this transaction.
		t.stage = stageReversing
		for i := range t.transfers {
			tr := &t.transfers[i]
			if tr.failCode == "" && !tr.applied {
				tr.failCode, tr.failMsg = CodeUndeliverable, "leg did not apply before recovery"
			}
			switch {
			case !tr.applied, tr.reversed:
				tr.done = true
			default:
				tr.done = false
				if !t.send(ctx, tr, tr.action.Opposite(), *tr.reversalKey) {
					tr.done, tr.reversalErr = true, "wallet actor unavailable"
				}
			}
		}
		logger.InfoLog.Printf("txn %s: resumed in reversing stage", t.transactionID)
	} else {
		t.stage = stageProcessing
		for i := range t.transfers {
			tr := &t.transfers[i]
			if tr.applied {
				tr.done = true
				continue
			}
			if !t.send(ctx, tr, tr.action, tr.key) {
				tr.done, tr.failCode, tr.failMsg = true, CodeClusterUnavailable, "wallet actor unavailable"
			}
		}
		logger.InfoLog.Printf("txn %s: resumed in processing stage", t.transactionID)
	}

	t.armTimeout(ctx)
	t.advance(ctx)
}

// fireTransfers sends every leg to its wallet actor. ctx.Request is
// non-blocking; replies arrive later as WalletResultMsg.
func (t *TransactionActor) fireTransfers(ctx actor.Context) {
	for i := range t.transfers {
		tr := &t.transfers[i]
		if !t.send(ctx, tr, tr.action, tr.key) {
			tr.done, tr.failCode, tr.failMsg = true, CodeClusterUnavailable, "wallet actor unavailable"
		}
	}
}

// fireReversals sends the compensating operation to every leg that was
// applied, under a fresh idempotency key so the wallet treats it as new work.
// Keys are persisted first so a recovery can finish the same reversal.
func (t *TransactionActor) fireReversals(ctx actor.Context) {
	t.stage = stageReversing

	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.applied && tr.reversalKey == nil {
			k := uuid.New()
			tr.reversalKey = &k
		}
	}
	t.persistPlan()

	for i := range t.transfers {
		tr := &t.transfers[i]
		if !tr.applied {
			tr.done = true // nothing to undo
			continue
		}
		tr.done = false
		if !t.send(ctx, tr, tr.action.Opposite(), *tr.reversalKey) {
			tr.done, tr.reversalErr = true, "wallet actor unavailable"
		}
	}
	t.armTimeout(ctx)
}

// send resolves the wallet PID and requests the operation. It returns false
// when the cluster cannot place the wallet actor.
func (t *TransactionActor) send(ctx actor.Context, tr *transfer, action models.LedgerType, key uuid.UUID) bool {
	pid, err := t.resolver.WalletPID(ctx, tr.walletID)
	if err != nil {
		return false
	}
	purpose := tr.purpose
	if action != tr.action {
		purpose = "reversal of txn " + t.transactionID.String()
	}
	txID := t.transactionID.String()
	if action == models.LedgerTypeCredit {
		ctx.Request(pid, &pb.CreditWalletMsg{WalletId: tr.walletID.String(), Amount: tr.amount, Purpose: purpose, TransactionId: txID, IdempotencyKey: key.String()})
	} else {
		ctx.Request(pid, &pb.DebitWalletMsg{WalletId: tr.walletID.String(), Amount: tr.amount, Purpose: purpose, TransactionId: txID, IdempotencyKey: key.String()})
	}
	return true
}

// collect records one wallet reply against the leg whose in-flight key matches.
func (t *TransactionActor) collect(ctx actor.Context, msg *pb.WalletResultMsg) {
	if t.stage != stageProcessing && t.stage != stageReversing {
		return
	}

	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.done || tr.inFlightKey(t.stage) != msg.IdempotencyKey {
			continue
		}
		tr.done = true
		t.replies++

		switch t.stage {
		case stageProcessing:
			if msg.ErrorCode == "" {
				tr.applied = true
				tr.initial, tr.updated, tr.seq = msg.InitialBalance, msg.UpdatedBalance, t.replies
			} else {
				tr.failCode = ErrorCode(msg.ErrorCode)
				tr.failMsg = msg.ErrorMessage
			}
		case stageReversing:
			if msg.ErrorCode == "" {
				tr.reversed = true
				tr.rInitial, tr.rUpdated, tr.rSeq = msg.InitialBalance, msg.UpdatedBalance, t.replies
			} else {
				tr.reversalErr = msg.ErrorCode + ": " + msg.ErrorMessage
			}
		}
		break
	}

	t.advance(ctx)
}

// timeout marks every leg still awaiting a reply in the given stage as failed
// and lets the lifecycle continue (reversal, then finalize).
func (t *TransactionActor) timeout(ctx actor.Context, armed stage) {
	if t.stage != armed || t.stage == stageFinalized {
		return
	}
	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.done {
			continue
		}
		tr.done = true
		switch t.stage {
		case stageProcessing:
			tr.failCode, tr.failMsg = CodeTimeout, "no reply within "+t.replyTimeout.String()
		case stageReversing:
			tr.reversalErr = "timeout: no reply within " + t.replyTimeout.String()
		}
		logger.WarningLog.Printf("txn %s: wallet %s timed out in stage %d", t.transactionID, tr.walletID, t.stage)
	}
	t.advance(ctx)
}

// armTimeout schedules a TransactionTimeoutMsg for the current stage.
func (t *TransactionActor) armTimeout(ctx actor.Context) {
	t.stopTimer()
	self, system, s, id := ctx.Self(), ctx.ActorSystem(), t.stage, t.transactionID.String()
	t.timer = time.AfterFunc(t.replyTimeout, func() {
		system.Root.Send(self, &pb.TransactionTimeoutMsg{TransactionId: id, Stage: int32(s)})
	})
}

func (t *TransactionActor) stopTimer() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// advance moves the lifecycle forward once every in-flight op has replied.
func (t *TransactionActor) advance(ctx actor.Context) {
	if !t.allDone() {
		return
	}
	switch t.stage {
	case stageProcessing:
		if t.anyFailed() {
			t.fireReversals(ctx)
			// Every leg may already be done (nothing applied, or no PIDs).
			if t.allDone() {
				ctx.Send(ctx.Self(), &pb.FinalizeTransactionMsg{TransactionId: t.transactionID.String()})
			}
		} else {
			ctx.Send(ctx.Self(), &pb.FinalizeTransactionMsg{TransactionId: t.transactionID.String()})
		}
	case stageReversing:
		ctx.Send(ctx.Self(), &pb.FinalizeTransactionMsg{TransactionId: t.transactionID.String()})
	}
}

func (t *TransactionActor) allDone() bool {
	for i := range t.transfers {
		if !t.transfers[i].done {
			return false
		}
	}
	return true
}

func (t *TransactionActor) anyFailed() bool {
	for i := range t.transfers {
		if t.transfers[i].failCode != "" {
			return true
		}
	}
	return false
}

func (t *TransactionActor) failAll(code ErrorCode, msg string) {
	for i := range t.transfers {
		t.transfers[i].done = true
		t.transfers[i].failCode = code
		t.transfers[i].failMsg = msg
	}
}

// persistPlan writes the legs (with any reversal keys) back to the row.
func (t *TransactionActor) persistPlan() {
	plan := make([]PlanEntry, 0, len(t.transfers))
	for i := range t.transfers {
		tr := &t.transfers[i]
		plan = append(plan, PlanEntry{WalletID: tr.walletID, Action: tr.action, Amount: tr.amount,
			Purpose: tr.purpose, IsFee: tr.isFee, IsInitial: tr.isInitial,
			IdempotencyKey: tr.key, ReversalKey: tr.reversalKey})
	}
	raw, err := EncodePlan(plan)
	if err == nil {
		err = t.db.Model(&models.Transaction{}).
			Where("id = ? AND created_at >= ?", t.transactionID, t.createdAt).
			Updates(map[string]any{"transfers": raw, "updated_at": time.Now()}).Error
	}
	if err != nil {
		logger.ErrorLog.Printf("txn %s: could not persist plan: %v", t.transactionID, err)
	}
}

// finalize writes the terminal status exactly once, notifies, and stops.
func (t *TransactionActor) finalize(ctx actor.Context) {
	if t.stage == stageFinalized || t.stage == stageIdle {
		return
	}
	t.stage = stageFinalized
	t.stopTimer()

	status := models.TransactionStatusSuccess
	narration := "Transaction processed successfully"

	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.failCode != "" {
			status = models.TransactionStatusFailed
			narration = humanMessage(tr.failCode, tr.failMsg)
			break
		}
	}

	unreconciled := false
	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.applied && status == models.TransactionStatusFailed && !tr.reversed {
			unreconciled = true
			logger.ErrorLog.Printf("txn %s: RECONCILIATION REQUIRED wallet %s %s %d not reversed: %s",
				t.transactionID, tr.walletID, tr.action, tr.amount, tr.reversalErr)
		}
	}
	if unreconciled {
		narration += " (reversal incomplete, reconciliation required)"
	}

	var err error
	for attempt := 1; attempt <= finalizeAttempts; attempt++ {
		now := time.Now()
		err = t.db.Model(&models.Transaction{}).
			Where("id = ? AND created_at >= ?", t.transactionID, t.createdAt).
			Updates(map[string]any{
				"status":         status,
				"narration":      narration,
				"date_completed": now,
				"is_completed":   true,
				"updated_at":     now,
			}).Error
		if err == nil {
			break
		}
		logger.WarningLog.Printf("txn %s: final status write attempt %d failed: %v", t.transactionID, attempt, err)
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}
	if err != nil {
		// The row stays in processing; the recovery sweeper will re-drive it,
		// find every leg settled in the ledger, and finalize again.
		logger.ErrorLog.Printf("txn %s: could not write final status %s, left for recovery: %v", t.transactionID, status, err)
	} else {
		logger.InfoLog.Printf("txn %s finalized: %s", t.transactionID, status)
		t.notify()
	}

	ctx.Poison(ctx.Self())
}

func (t *TransactionActor) notify() {
	var tx models.Transaction
	err := t.db.Where("id = ? AND created_at >= ?", t.transactionID, t.createdAt).First(&tx).Error
	if err != nil {
		logger.ErrorLog.Printf("txn %s: could not load for notification: %v", t.transactionID, err)
		return
	}
	t.notifier.Notify(tx, t.movements(tx.Currency))
}

// movements folds the balances observed in wallet replies into one entry per
// wallet: initial from the earliest reply, updated from the latest. Nil when
// nothing was observed (resumed transactions), so the notifier uses the ledger.
func (t *TransactionActor) movements(currencyCode string) []models.WalletMovement {
	type point struct {
		seq              int
		initial, updated int64
	}
	byWallet := map[uuid.UUID][]point{}
	order := make([]uuid.UUID, 0, len(t.transfers))
	observed := false

	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.applied && tr.seq > 0 {
			if _, ok := byWallet[tr.walletID]; !ok {
				order = append(order, tr.walletID)
			}
			byWallet[tr.walletID] = append(byWallet[tr.walletID], point{tr.seq, tr.initial, tr.updated})
			observed = true
		}
		if tr.reversed && tr.rSeq > 0 {
			if _, ok := byWallet[tr.walletID]; !ok {
				order = append(order, tr.walletID)
			}
			byWallet[tr.walletID] = append(byWallet[tr.walletID], point{tr.rSeq, tr.rInitial, tr.rUpdated})
			observed = true
		}
		// A leg found already applied on resume has no observed balances.
		if tr.applied && tr.seq == 0 {
			return nil
		}
	}
	if !observed {
		return nil
	}

	out := make([]models.WalletMovement, 0, len(order))
	for _, id := range order {
		pts := byWallet[id]
		first, last := pts[0], pts[0]
		for _, p := range pts[1:] {
			if p.seq < first.seq {
				first = p
			}
			if p.seq > last.seq {
				last = p
			}
		}
		out = append(out, models.WalletMovement{
			WalletID: id, Currency: currencyCode,
			InitialBalance: first.initial, UpdatedBalance: last.updated, Entries: len(pts),
		})
	}
	return out
}
