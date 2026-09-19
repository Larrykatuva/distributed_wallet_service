// Package recovery re-drives transactions whose orchestration was interrupted:
// dead-lettered actor messages, crashed or rebalanced TransactionActors, or
// rows that simply stopped moving. It reconstructs progress from the persisted
// plan and the ledger rather than guessing.
package recovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/asynkron/protoactor-go/cluster"
	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/actors"
	"github.com/katuva/wallet/internal/models"
	pb "github.com/katuva/wallet/proto/actors"
	"gorm.io/gorm"
)

// Options tune the manager. Zero values take the defaults below.
type Options struct {
	// MaxAttempts is how many times one transaction may be re-driven before
	// it is marked failed for manual reconciliation.
	MaxAttempts int
	// Cooldown suppresses repeated recovery of the same transaction within
	// the window (dead letters tend to arrive in bursts).
	Cooldown time.Duration
	// StaleAfter is how long an incomplete transaction may go without an
	// update before the sweeper re-drives it.
	StaleAfter time.Duration
	// SweepInterval is how often the sweeper runs.
	SweepInterval time.Duration
	// Lookback bounds created_at so partition pruning applies to sweeps.
	Lookback time.Duration
}

func (o Options) withDefaults() Options {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	if o.Cooldown <= 0 {
		o.Cooldown = 10 * time.Second
	}
	if o.StaleAfter <= 0 {
		o.StaleAfter = 2 * time.Minute
	}
	if o.SweepInterval <= 0 {
		o.SweepInterval = 30 * time.Second
	}
	if o.Lookback <= 0 {
		o.Lookback = 7 * 24 * time.Hour
	}
	return o
}

// Manager implements actors.Recoverer.
type Manager struct {
	db       *gorm.DB
	cluster  *cluster.Cluster
	notifier actors.Notifier
	opts     Options

	mu       sync.Mutex
	attempts map[uuid.UUID]int
	lastRun  map[uuid.UUID]time.Time
}

func NewManager(db *gorm.DB, c *cluster.Cluster, notifier actors.Notifier, opts Options) *Manager {
	if notifier == nil {
		notifier = actors.NoopNotifier{}
	}
	return &Manager{
		db: db, cluster: c, notifier: notifier, opts: opts.withDefaults(),
		attempts: map[uuid.UUID]int{}, lastRun: map[uuid.UUID]time.Time{},
	}
}

// Recover schedules a re-drive of the transaction. It returns immediately;
// dead-letter handling runs on the actor system's event stream and must not
// block. Repeated requests inside the cooldown are coalesced.
func (m *Manager) Recover(txID uuid.UUID, reason string) {
	m.mu.Lock()
	if last, ok := m.lastRun[txID]; ok && time.Since(last) < m.opts.Cooldown {
		m.mu.Unlock()
		return
	}
	m.lastRun[txID] = time.Now()
	m.attempts[txID]++
	attempt := m.attempts[txID]
	m.mu.Unlock()

	go func() {
		if err := m.recover(txID, reason, attempt); err != nil {
			logger.ErrorLog.Printf("recovery txn=%s attempt %d (%s): %v", txID, attempt, reason, err)
		}
	}()
}

func (m *Manager) recover(txID uuid.UUID, reason string, attempt int) error {
	var tx models.Transaction
	err := m.db.Where("id = ? AND created_at >= ?", txID, time.Now().Add(-m.opts.Lookback)).First(&tx).Error
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if tx.IsCompleted {
		m.forget(txID)
		return nil
	}

	if attempt > m.opts.MaxAttempts {
		return m.giveUp(tx, reason)
	}

	plan, err := actors.DecodePlan(tx.Transfers)
	if err != nil {
		return err
	}
	if len(plan) == 0 {
		// Pre-plan row: nothing can have moved (no keys, no ledger match).
		return m.markFailed(tx, "no transfer plan recorded; cannot recover", false)
	}

	pid := m.cluster.Get(txID.String(), actors.KindTransaction)
	if pid == nil {
		return fmt.Errorf("cluster could not place transaction actor")
	}

	logger.InfoLog.Printf("recovery txn=%s attempt %d: re-driving (%s)", txID, attempt, reason)
	m.cluster.ActorSystem.Root.Send(pid, &pb.StartTransactionMsg{
		TransactionId: txID.String(),
		CreatedAtUnix: tx.CreatedAt.Add(-time.Minute).Unix(),
		Transfers:     actors.PlanToProto(plan),
		Resume:        true,
	})
	return nil
}

// giveUp stops retrying. If ledger rows exist for the transaction, money may
// have moved and the row is flagged for manual reconciliation.
func (m *Manager) giveUp(tx models.Transaction, reason string) error {
	var moved int64
	m.db.Model(&models.Ledger{}).
		Where("transaction_id = ? AND created_at >= ?", tx.ID, tx.CreatedAt.Add(-time.Minute)).
		Count(&moved)
	msg := fmt.Sprintf("processing abandoned after %d recovery attempts (%s)", m.opts.MaxAttempts, reason)
	return m.markFailed(tx, msg, moved > 0)
}

func (m *Manager) markFailed(tx models.Transaction, narration string, reconcile bool) error {
	if reconcile {
		narration += " (ledger entries exist, reconciliation required)"
		logger.ErrorLog.Printf("recovery txn=%s: RECONCILIATION REQUIRED: %s", tx.ID, narration)
	}
	now := time.Now()
	err := m.db.Model(&models.Transaction{}).
		Where("id = ? AND created_at >= ? AND is_completed = FALSE", tx.ID, tx.CreatedAt.Add(-time.Minute)).
		Updates(map[string]any{
			"status": models.TransactionStatusFailed, "narration": narration,
			"is_completed": true, "date_completed": now, "updated_at": now,
		}).Error
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	m.forget(tx.ID)

	tx.Status, tx.Narration, tx.IsCompleted, tx.DateCompleted = models.TransactionStatusFailed, narration, true, &now
	m.notifier.Notify(tx, nil)
	return nil
}

func (m *Manager) forget(txID uuid.UUID) {
	m.mu.Lock()
	delete(m.attempts, txID)
	delete(m.lastRun, txID)
	m.mu.Unlock()
}

// RunSweeper periodically re-drives incomplete transactions that have not
// been updated for StaleAfter. It catches everything the dead-letter path
// cannot see: a node that died mid-transaction, a lost final write, or a
// timeout message that never fired.
func (m *Manager) RunSweeper(ctx context.Context) {
	ticker := time.NewTicker(m.opts.SweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sweep()
		}
	}
}

func (m *Manager) sweep() {
	var stuck []uuid.UUID
	err := m.db.Model(&models.Transaction{}).
		Where("is_completed = FALSE AND updated_at < ? AND created_at >= ?",
			time.Now().Add(-m.opts.StaleAfter), time.Now().Add(-m.opts.Lookback)).
		Limit(200).
		Pluck("id", &stuck).Error
	if err != nil {
		logger.ErrorLog.Printf("recovery sweep: %v", err)
		return
	}
	if len(stuck) > 0 {
		logger.WarningLog.Printf("recovery sweep: %d stuck transaction(s)", len(stuck))
	}
	for _, id := range stuck {
		m.Recover(id, "stale for over "+m.opts.StaleAfter.String())
	}
}
