package actors

import (
	"errors"
	"fmt"
)

// Kind names registered with the cluster. Grain identity is the entity UUID.
const (
	KindWallet      = "Wallet"
	KindTransaction = "Transaction"
)

// ErrorCode is the machine-readable failure reason carried in WalletResultMsg.
// It crosses node boundaries as a string, so it must not be a Go error value.
type ErrorCode string

const (
	CodeInsufficientFunds  ErrorCode = "insufficient_funds"
	CodeInactiveWallet     ErrorCode = "inactive_wallet"
	CodeWalletNotFound     ErrorCode = "wallet_not_found"
	CodeWalletMismatch     ErrorCode = "wallet_mismatch"
	CodeChecksumMismatch   ErrorCode = "checksum_mismatch"
	CodeVersionConflict    ErrorCode = "version_conflict"
	CodeInvalidMessage     ErrorCode = "invalid_message"
	CodeClusterUnavailable ErrorCode = "cluster_unavailable"
	CodeUndeliverable      ErrorCode = "undeliverable"
	CodeTimeout            ErrorCode = "timeout"
	CodeInternal           ErrorCode = "internal_error"
)

// WalletError is returned by WalletActor operations and mapped onto the
// error_code / error_message fields of WalletResultMsg.
type WalletError struct {
	Code    ErrorCode
	Message string
}

func (e *WalletError) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func newWalletError(code ErrorCode, msg string) *WalletError {
	return &WalletError{Code: code, Message: msg}
}

func wrapInternal(err error) *WalletError {
	var we *WalletError
	if errors.As(err, &we) {
		return we
	}
	return &WalletError{Code: CodeInternal, Message: err.Error()}
}

// humanMessage is what ends up in transaction.narration for a failure.
func humanMessage(code ErrorCode, msg string) string {
	switch code {
	case CodeInsufficientFunds:
		return "insufficient funds"
	case CodeInactiveWallet:
		return "inactive wallet"
	case CodeWalletNotFound:
		return "wallet not found"
	case CodeChecksumMismatch:
		return "wallet integrity check failed"
	case CodeVersionConflict:
		return "wallet was modified concurrently"
	case CodeClusterUnavailable:
		return "wallet actor unavailable"
	case CodeUndeliverable:
		return "wallet message could not be delivered"
	case CodeTimeout:
		return "wallet did not respond in time"
	default:
		if msg != "" {
			return msg
		}
		return string(code)
	}
}
