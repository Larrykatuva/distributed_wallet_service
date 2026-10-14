package actors

import (
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/cache"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/models"
	pb "github.com/katuva/wallet/proto/actors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// stage is the TransactionActor lifecycle. Transitions only move forward:
//
//	idle → processing (debits) → crediting (credits) → finalized
//	       any refusal in either → reversing → finalized
//
// Credits are sent only once every debit has applied, so a refused debit
// never leaves money with a recipient that has to be clawed back.
type stage int

const (
	stageIdle stage = iota
	stageProcessing
	stageReversing
	stageFinalized
	stageCrediting
)

// DefaultReplyTimeout is the tick at which the orchestrator re-sends legs that
// need it and reports it is alive. A slow leg is never failed.
const DefaultReplyTimeout = 30 * time.Second

// maxLogEvery caps the backoff, in ticks, of the "still waiting" log line.
const maxLogEvery = 16

const finalizeAttempts = 3

// reversalLookback bounds how far back a reversal looks for its original
// (mirrors the service lookup window) so Postgres can prune partitions.
const reversalLookback = 400 * 24 * time.Hour

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
	retry   bool // re-send at the next tick; set only when no copy is in flight

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

// TransactionActor orchestrates one transaction: it applies the debits, then
// the credits, on the owning WalletActors, reverses applied legs if any leg
// failed, writes the final status, notifies, and then stops itself.
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
	ticks         int // timer ticks in the current stage
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
		t.tick(ctx, stage(msg.Stage))

	case *pb.FinalizeTransactionMsg:
		t.finalize(ctx)

	case *actor.Stopping:
		t.stopTimer()
	}
}

func (t *TransactionActor) start(ctx actor.Context, msg *pb.StartTransactionMsg) {
	if t.stage != stageIdle {
		if msg.Resume && t.working() {
			logger.WarningLog.Printf("txn %s: resume while in progress (stage %d), still waiting", msg.TransactionId, t.stage)
			t.resendPending(ctx)
			t.heartbeat()
			return
		}
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
		Scopes(t.ownRow).Where("status IN ?", allowed).
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
	t.fireLegs(ctx)
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
		Where("transaction_id = ? AND created_at >= ? AND created_at < ?", t.transactionID, t.createdAt, time.Now().Add(24*time.Hour)).
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
		persist := false
		for i := range t.transfers {
			tr := &t.transfers[i]
			if tr.failCode == "" && !tr.applied {
				tr.failCode, tr.failMsg = CodeUndeliverable, "leg did not apply before recovery"
			}
			if tr.applied && !tr.reversed && tr.reversalKey == nil {
				k := uuid.New()
				tr.reversalKey, persist = &k, true
			}
		}
		if persist {
			t.persistPlan()
		}
		for i := range t.transfers {
			tr := &t.transfers[i]
			switch {
			case !tr.applied, tr.reversed:
				tr.done = true
			default:
				tr.done = false
				tr.retry = !t.send(ctx, tr, tr.action.Opposite(), *tr.reversalKey)
			}
		}
		logger.InfoLog.Printf("txn %s: resumed in reversing stage", t.transactionID)
	} else {
		t.stage = stageProcessing
		if t.debitsApplied() {
			t.stage = stageCrediting
		}
		for i := range t.transfers {
			tr := &t.transfers[i]
			if tr.applied {
				tr.done = true
			}
		}
		t.fireLegs(ctx)
		logger.InfoLog.Printf("txn %s: resumed in stage %d", t.transactionID, t.stage)
	}

	t.armTimeout(ctx)
	t.advance(ctx)
}

// fireLegs sends the current stage's legs (debits, or credits) that have not
// applied yet. ctx.Request is non-blocking; replies arrive later as
// WalletResultMsg. A leg that cannot be sent is retried on the next tick.
func (t *TransactionActor) fireLegs(ctx actor.Context) {
	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.done || !t.inStage(tr) {
			continue
		}
		tr.retry = !t.send(ctx, tr, tr.action, tr.key)
	}
}

// fireCredits starts the credit stage once every debit has applied.
func (t *TransactionActor) fireCredits(ctx actor.Context) {
	t.stage = stageCrediting
	t.ticks = 0
	t.fireLegs(ctx)
	t.armTimeout(ctx)
}

// inStage reports whether a leg is worked on in the current stage.
func (t *TransactionActor) inStage(tr *transfer) bool {
	switch t.stage {
	case stageProcessing:
		return tr.action == models.LedgerTypeDebit
	case stageCrediting:
		return tr.action == models.LedgerTypeCredit
	}
	return true
}

func (t *TransactionActor) working() bool {
	return t.stage == stageProcessing || t.stage == stageCrediting || t.stage == stageReversing
}

func (t *TransactionActor) debitsApplied() bool {
	for i := range t.transfers {
		if t.transfers[i].action == models.LedgerTypeDebit && !t.transfers[i].applied {
			return false
		}
	}
	return true
}

// fireReversals sends the compensating operation to every leg that was
// applied, under a fresh idempotency key so the wallet treats it as new work.
// Keys are persisted first so a recovery can finish the same reversal.
func (t *TransactionActor) fireReversals(ctx actor.Context) {
	t.stage = stageReversing
	t.ticks = 0

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
			tr.done, tr.retry = true, false // nothing to undo
			continue
		}
		tr.done = false
		tr.retry = !t.send(ctx, tr, tr.action.Opposite(), *tr.reversalKey)
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
// A leg settles only on success or a definitive refusal. Any other answer means
// that copy is finished without a verdict, so the leg is re-sent under its key.
func (t *TransactionActor) collect(ctx actor.Context, msg *pb.WalletResultMsg) {
	if !t.working() {
		return
	}
	code := ErrorCode(msg.ErrorCode)

	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.inFlightKey(t.stage) != msg.IdempotencyKey {
			continue
		}
		switch {
		case tr.done:
			if code == "" && tr.failCode != "" && !tr.applied {
				t.lateApply(ctx, tr, msg)
			}
		case code == "":
			tr.done = true
			t.replies++
			if t.stage == stageReversing {
				tr.reversed = true
				tr.rInitial, tr.rUpdated, tr.rSeq = msg.InitialBalance, msg.UpdatedBalance, t.replies
			} else {
				tr.applied = true
				tr.initial, tr.updated, tr.seq = msg.InitialBalance, msg.UpdatedBalance, t.replies
			}
		case code.Definitive():
			tr.done = true
			t.replies++
			if t.stage == stageReversing {
				tr.reversalErr = msg.ErrorCode + ": " + msg.ErrorMessage
			} else {
				tr.failCode, tr.failMsg = code, msg.ErrorMessage
			}
		default:
			tr.retry = true
			logger.WarningLog.Printf("txn %s: wallet %s answered %s (%s), re-sending next tick",
				t.transactionID, tr.walletID, code, msg.ErrorMessage)
		}
		break
	}

	t.advance(ctx)
}

// lateApply handles a leg applied after it was refused (e.g. a resumed
// orchestrator re-sent it while the original was queued): it gets reversed.
func (t *TransactionActor) lateApply(ctx actor.Context, tr *transfer, msg *pb.WalletResultMsg) {
	logger.ErrorLog.Printf("txn %s: wallet %s applied %s %d after refusing it; reversing it",
		t.transactionID, tr.walletID, tr.action, tr.amount)
	t.replies++
	tr.applied = true
	tr.initial, tr.updated, tr.seq = msg.InitialBalance, msg.UpdatedBalance, t.replies
	if t.stage != stageReversing {
		return // fireReversals undoes it with the other applied legs
	}
	k := uuid.New()
	tr.reversalKey = &k
	t.persistPlan()
	tr.done = false
	tr.retry = !t.send(ctx, tr, tr.action.Opposite(), k)
}

// tick runs every replyTimeout while legs are outstanding. Nothing fails here
// and a leg still in flight is never re-sent: a slow wallet will answer, and a
// second copy could apply after the first was refused. updated_at is refreshed
// so the recovery sweeper leaves this live transaction alone.
func (t *TransactionActor) tick(ctx actor.Context, armed stage) {
	if t.stage != armed || !t.working() {
		return
	}
	t.ticks++
	pending := t.resendPending(ctx)
	if pending == 0 {
		return
	}
	if t.ticks >= 4 && (t.ticks&(t.ticks-1) == 0 || t.ticks%maxLogEvery == 0) {
		logger.WarningLog.Printf("txn %s: %d leg(s) still awaiting wallet replies after %s (stage %d)",
			t.transactionID, pending, time.Duration(t.ticks)*t.replyTimeout, t.stage)
	}
	t.heartbeat()
	t.armTimeout(ctx)
}

// resendPending re-sends the legs marked for retry and returns how many legs
// are outstanding.
func (t *TransactionActor) resendPending(ctx actor.Context) int {
	pending := 0
	for i := range t.transfers {
		tr := &t.transfers[i]
		if tr.done || !t.inStage(tr) {
			continue
		}
		pending++
		if !tr.retry {
			continue
		}
		action, key := tr.action, tr.key
		if t.stage == stageReversing {
			if tr.reversalKey == nil {
				continue
			}
			action, key = tr.action.Opposite(), *tr.reversalKey
		}
		tr.retry = !t.send(ctx, tr, action, key)
	}
	return pending
}

// ownRow scopes a query to this transaction's row. createdAt is its created_at
// less a minute, so the window covers one partition (two at a month boundary).
func (t *TransactionActor) ownRow(db *gorm.DB) *gorm.DB {
	return db.Where("id = ? AND created_at >= ? AND created_at < ?", t.transactionID, t.createdAt, t.createdAt.Add(2*time.Minute))
}

func (t *TransactionActor) heartbeat() {
	err := t.db.Model(&models.Transaction{}).
		Scopes(t.ownRow).Where("is_completed = FALSE").
		Update("updated_at", time.Now()).Error
	if err != nil {
		logger.WarningLog.Printf("txn %s: heartbeat: %v", t.transactionID, err)
	}
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
	case stageProcessing, stageCrediting:
		switch {
		case t.anyFailed():
			t.fireReversals(ctx)
		case t.stage == stageProcessing:
			t.fireCredits(ctx)
		}
		// The next stage may have nothing in flight (nothing to undo, or
		// every credit already applied).
		if t.allDone() {
			ctx.Send(ctx.Self(), &pb.FinalizeTransactionMsg{TransactionId: t.transactionID.String()})
		}
	case stageReversing:
		ctx.Send(ctx.Self(), &pb.FinalizeTransactionMsg{TransactionId: t.transactionID.String()})
	}
}

// allDone reports whether every leg of the current stage has its answer.
func (t *TransactionActor) allDone() bool {
	for i := range t.transfers {
		if !t.transfers[i].done && t.inStage(&t.transfers[i]) {
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
			Scopes(t.ownRow).
			Updates(map[string]any{"transfers": raw, "updated_at": time.Now()}).Error
	}
	if err != nil {
		logger.ErrorLog.Printf("txn %s: could not persist plan: %v", t.transactionID, err)
	}
}

// finalize writes the terminal status exactly once, notifies, and stops.
func (t *TransactionActor) finalize(ctx actor.Context) {
	if t.stage == stageFinalized || t.stage == stageIdle || !t.allDone() {
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

	var (
		err      error
		row      models.Transaction
		original *models.Transaction
		written  bool
		attempt  int
	)
	for attempt = 1; attempt <= finalizeAttempts; attempt++ {
		row, original, written = models.Transaction{}, nil, false
		err = t.db.Transaction(func(db *gorm.DB) error {
			now := time.Now()
			// One outcome per transaction; RETURNING * feeds the callback.
			res := db.Model(&row).
				Clauses(clause.Returning{}).
				Scopes(t.ownRow).Where("is_completed = FALSE").
				Updates(map[string]any{
					"status":         status,
					"narration":      narration,
					"date_completed": now,
					"is_completed":   true,
					"updated_at":     now,
				})
			if res.Error != nil {
				return res.Error
			}
			written = res.RowsAffected > 0
			if written && status == models.TransactionStatusSuccess && row.Type == models.TransactionTypeReversal && row.ProviderRef != nil {
				original, err = t.markReversed(db, *row.ProviderRef, now)
				return err
			}
			return nil
		})
		if err == nil {
			break
		}
		logger.WarningLog.Printf("txn %s: final status write attempt %d failed: %v", t.transactionID, attempt, err)
		time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
	}

	switch {
	case err != nil:
		// The row stays in processing; the recovery sweeper will re-drive it,
		// find every leg settled in the ledger, and finalize again.
		logger.ErrorLog.Printf("txn %s: could not write final status %s, left for recovery: %v", t.transactionID, status, err)
	case !written:
		t.alreadyFinal(status, attempt > 1)
	default:
		logger.InfoLog.Printf("txn %s finalized: %s", t.transactionID, status)
		if original != nil {
			keys := []string{cache.TransactionKey(original.ID.String()), cache.TransactionRRNKey(original.RRN)}
			if original.OrderID != nil {
				keys = append(keys, cache.TransactionOrderKey(*original.OrderID))
			}
			cache.Delete(keys...)
		}
		t.notifier.Notify(row, t.movements(row.Currency))
	}

	ctx.Poison(ctx.Self())
}

// alreadyFinal handles a finalize that found the row completed: by an earlier
// attempt whose commit acknowledgement was lost, or by recovery. The outcome
// already written stands.
func (t *TransactionActor) alreadyFinal(status models.TransactionStatus, retried bool) {
	var row models.Transaction
	err := t.db.Scopes(t.ownRow).First(&row).Error
	switch {
	case err != nil:
		logger.ErrorLog.Printf("txn %s: final status %s not written: %v", t.transactionID, status, err)
	case row.Status != status:
		logger.ErrorLog.Printf("txn %s: RECONCILIATION REQUIRED outcome %s not written, already completed as %s",
			t.transactionID, status, row.Status)
	case retried:
		// An earlier attempt committed but its callback was never sent. For a
		// reversal, the original's id key expires on its TTL.
		if row.Type == models.TransactionTypeReversal && row.ProviderRef != nil {
			keys := []string{cache.TransactionRRNKey(*row.ProviderRef)}
			if row.OrderID != nil {
				keys = append(keys, cache.TransactionOrderKey(*row.OrderID))
			}
			cache.Delete(keys...)
		}
		logger.InfoLog.Printf("txn %s finalized: %s", t.transactionID, status)
		t.notifier.Notify(row, t.movements(row.Currency))
	}
}

// markReversed flips the transaction a successful reversal undid (named by
// RRN in the reversal's provider_ref) from success to reversed. It runs in
// the same database transaction as the reversal's final status, so the two
// cannot disagree. Finding nothing to flip means the original was already
// reversed or changed underneath: money moved anyway, so it is flagged.
func (t *TransactionActor) markReversed(db *gorm.DB, rrn string, now time.Time) (*models.Transaction, error) {
	var original models.Transaction
	res := db.Model(&original).
		Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}, {Name: "rrn"}, {Name: "order_id"}}}).
		// The original predates the reversal (t.createdAt is the reversal's
		// creation less a minute), so future partitions are skipped too.
		Where("rrn = ? AND status = ? AND created_at >= ? AND created_at < ?", rrn, models.TransactionStatusSuccess,
			t.createdAt.Add(-reversalLookback), t.createdAt.Add(time.Minute)).
		Updates(map[string]any{
			"status":     models.TransactionStatusReversed,
			"narration":  "Transaction reversed",
			"updated_at": now,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		logger.ErrorLog.Printf("txn %s: RECONCILIATION REQUIRED reversal settled but original rrn %s was not in success", t.transactionID, rrn)
		return nil, nil
	}
	return &original, nil
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
