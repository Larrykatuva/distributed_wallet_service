// Package webhook delivers transaction completion callbacks to the URL the
// caller supplied on initiation.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/katuva/wallet/dpk/logger"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
)

const (
	defaultTimeout = 10 * time.Second
	maxAttempts    = 3
)

// Notifier POSTs the final transaction state to its callback_url.
// Delivery is best-effort: retried with backoff, then logged and dropped.
type Notifier struct {
	client *http.Client
	build  func(models.Transaction, []models.WalletMovement) types.TransactionResDto
}

// NewNotifier takes the payload builder so the callback carries the same
// shape as the API (transfers, per-wallet balances).
func NewNotifier(build func(models.Transaction, []models.WalletMovement) types.TransactionResDto) *Notifier {
	if build == nil {
		build = func(tx models.Transaction, _ []models.WalletMovement) types.TransactionResDto {
			return types.TransactionFromModel(tx)
		}
	}
	return &Notifier{client: &http.Client{Timeout: defaultTimeout}, build: build}
}

// Notify returns immediately; payload building and delivery happen off the
// actor's goroutine.
func (n *Notifier) Notify(tx models.Transaction, movements []models.WalletMovement) {
	if tx.CallbackURL == nil || *tx.CallbackURL == "" {
		return
	}
	go n.deliver(*tx.CallbackURL, n.build(tx, movements))
}

func (n *Notifier) deliver(url string, payload types.TransactionResDto) {
	body, err := json.Marshal(payload)
	if err != nil {
		logger.ErrorLog.Printf("webhook txn=%s: marshal: %v", payload.Rrn, err)
		return
	}

	backoff := time.Second
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if n.post(url, body) {
			return
		}
		if attempt < maxAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}
	logger.ErrorLog.Printf("webhook txn=%s: giving up after %d attempts to %s", payload.Rrn, maxAttempts, url)
}

func (n *Notifier) post(url string, body []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		logger.ErrorLog.Printf("webhook: build request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "katuva-wallet/1.0")

	resp, err := n.client.Do(req)
	if err != nil {
		logger.WarningLog.Printf("webhook %s: %v", url, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true
	}
	logger.WarningLog.Printf("webhook %s: status %d", url, resp.StatusCode)
	return false
}
