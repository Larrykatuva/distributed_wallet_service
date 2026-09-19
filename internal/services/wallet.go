package services

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/cache"
	"github.com/katuva/wallet/internal/actors"
	"github.com/katuva/wallet/internal/currency"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

type WalletService struct {
	db       *gorm.DB
	profiles *ProfileService
}

func NewWalletService(db *gorm.DB) *WalletService {
	return &WalletService{db: db, profiles: NewProfileService(db)}
}

// Cache TTLs. Wallet reads carry balances and are invalidated by the
// WalletActor on every balance change; the short TTL bounds the one race a
// read-then-write can leave behind. Metadata (owner, currency, status)
// changes rarely and is re-validated by the actor at apply time.
const (
	walletReadTTL = 5 * time.Second
	walletMetaTTL = 5 * time.Minute
)

// selectProfileName keeps association loads to the two columns the DTO uses.
func selectProfileName(db *gorm.DB) *gorm.DB { return db.Select("id", "full_name") }

// Get returns a wallet with its profile and merchant loaded.
func (w *WalletService) Get(id uuid.UUID) (types.WalletResDto, error) {
	return cache.Remember(cache.WalletKey(id.String()), walletReadTTL, func() (types.WalletResDto, error) {
		var wallet models.Wallet
		err := w.db.Preload("Profile", selectProfileName).Preload("Merchant", selectProfileName).
			First(&wallet, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return types.WalletResDto{}, notFound("wallet not found")
		}
		if err != nil {
			return types.WalletResDto{}, fmt.Errorf("get wallet: %w", err)
		}
		return walletToDto(wallet), nil
	})
}

// GetByNumber returns a wallet by its human-readable number (e.g. W100077).
func (w *WalletService) GetByNumber(number string) (types.WalletResDto, error) {
	number = strings.ToUpper(strings.TrimSpace(number))
	return cache.Remember(cache.WalletNumberKey(number), walletReadTTL, func() (types.WalletResDto, error) {
		var wallet models.Wallet
		err := w.db.Preload("Profile", selectProfileName).Preload("Merchant", selectProfileName).
			First(&wallet, "number = ?", number).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return types.WalletResDto{}, notFound("wallet not found")
		}
		if err != nil {
			return types.WalletResDto{}, fmt.Errorf("get wallet: %w", err)
		}
		return walletToDto(wallet), nil
	})
}

// List returns wallets newest first, filtered and paginated.
func (w *WalletService) List(f types.WalletFilter, page types.Pagination) (types.Page[types.WalletResDto], error) {
	page = page.Normalize()
	q := w.db.Model(&models.Wallet{})
	if f.MerchantID != nil {
		q = q.Where("merchant_id = ?", *f.MerchantID)
	}
	if f.ProfileID != nil {
		q = q.Where("profile_id = ?", *f.ProfileID)
	}
	if f.Currency != nil {
		q = q.Where("currency = ?", currency.Normalize(*f.Currency))
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return types.Page[types.WalletResDto]{}, fmt.Errorf("count wallets: %w", err)
	}
	var rows []models.Wallet
	err := q.Preload("Profile", selectProfileName).Preload("Merchant", selectProfileName).
		Order("created_at DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&rows).Error
	if err != nil {
		return types.Page[types.WalletResDto]{}, fmt.Errorf("list wallets: %w", err)
	}
	out := make([]types.WalletResDto, 0, len(rows))
	for _, r := range rows {
		out = append(out, walletToDto(r))
	}
	return types.NewPage(out, page, total), nil
}

func (w *WalletService) Create(payload types.WalletReqDto) (types.WalletResDto, error) {
	profiles, err := w.profiles.FindByIDs([]uuid.UUID{payload.ProfileID, payload.MerchantID})
	if err != nil {
		return types.WalletResDto{}, err
	}
	profile, ok := profiles[payload.ProfileID]
	if !ok {
		return types.WalletResDto{}, notFound("profile not found")
	}
	merchant, ok := profiles[payload.MerchantID]
	if !ok {
		return types.WalletResDto{}, notFound("merchant not found")
	}

	code, ok := currency.Lookup(payload.Currency)
	if !ok {
		return types.WalletResDto{}, invalid(fmt.Sprintf("currency %q is not a supported ISO 4217 code (see GET /v1/currencies)", payload.Currency))
	}

	wallet := models.Wallet{
		Currency:   code.Code,
		ProfileID:  profile.ID,
		MerchantID: &merchant.ID,
		Status:     models.WalletStatusActive,
	}

	err = w.db.Transaction(func(tx *gorm.DB) error {
		// `number` comes from a DB sequence default, so it is omitted here.
		if err := tx.Omit("number").Create(&wallet).Error; err != nil {
			return err
		}
		// Stamp the integrity checksum on the freshly assigned ID/version.
		if err := tx.First(&wallet, "id = ?", wallet.ID).Error; err != nil {
			return err
		}
		wallet.Checksum = actors.ComputeChecksum(&wallet)
		return tx.Model(&wallet).Update("checksum", wallet.Checksum).Error
	})
	if err != nil {
		return types.WalletResDto{}, fmt.Errorf("create wallet: %w", err)
	}

	wallet.Profile = profile
	wallet.Merchant = &merchant
	dto := walletToDto(wallet)
	cache.Set(cache.WalletKey(wallet.ID.String()), dto, walletReadTTL)
	cache.Set(cache.WalletNumberKey(wallet.Number), dto, walletReadTTL)
	cache.Set(cache.WalletMetaKey(wallet.ID.String()), metaFromWallet(wallet), walletMetaTTL)
	return dto, nil
}

// walletMeta is the balance-free view of a wallet used to validate a
// transaction request. It is what gets cached: balances never are.
type walletMeta struct {
	ID           uuid.UUID           `json:"id"`
	Number       string              `json:"number"`
	ProfileID    uuid.UUID           `json:"profile_id"`
	MerchantID   *uuid.UUID          `json:"merchant_id"`
	Status       models.WalletStatus `json:"status"`
	Currency     string              `json:"currency"`
	ProfileName  string              `json:"profile_name"`
	MerchantName string              `json:"merchant_name"`
}

func metaFromWallet(w models.Wallet) walletMeta {
	m := walletMeta{ID: w.ID, Number: w.Number, ProfileID: w.ProfileID, MerchantID: w.MerchantID,
		Status: w.Status, Currency: w.Currency, ProfileName: w.Profile.FullName}
	if w.Merchant != nil {
		m.MerchantName = w.Merchant.FullName
	}
	return m
}

func (m walletMeta) toWallet() models.Wallet {
	w := models.Wallet{ID: m.ID, Number: m.Number, ProfileID: m.ProfileID, MerchantID: m.MerchantID,
		Status: m.Status, Currency: m.Currency, Profile: models.Profile{ID: m.ProfileID, FullName: m.ProfileName}}
	if m.MerchantID != nil {
		w.Merchant = &models.Profile{ID: *m.MerchantID, FullName: m.MerchantName}
	}
	return w
}

// ValidateWallets loads the given wallets' metadata and checks they are all
// active. Cached entries are used where present; the rest are fetched in one
// query selecting only the columns needed. Balances are deliberately absent:
// the WalletActor is the only authority on those.
func (w *WalletService) ValidateWallets(ids []uuid.UUID) (map[uuid.UUID]models.Wallet, error) {
	if len(ids) == 0 {
		return nil, invalid("no wallets supplied")
	}

	byID := make(map[uuid.UUID]models.Wallet, len(ids))
	missing := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, dup := byID[id]; dup {
			continue
		}
		var m walletMeta
		if cache.Get(cache.WalletMetaKey(id.String()), &m) {
			byID[id] = m.toWallet()
		} else {
			missing = append(missing, id)
		}
	}

	if len(missing) > 0 {
		var wallets []models.Wallet
		err := w.db.Select("id", "number", "profile_id", "merchant_id", "status", "currency").
			Where("id IN ?", missing).
			Preload("Profile", selectProfileName).Preload("Merchant", selectProfileName).
			Find(&wallets).Error
		if err != nil {
			return nil, fmt.Errorf("load wallets: %w", err)
		}
		for _, wl := range wallets {
			byID[wl.ID] = wl
			cache.Set(cache.WalletMetaKey(wl.ID.String()), metaFromWallet(wl), walletMetaTTL)
		}
	}

	for _, id := range ids {
		wl, ok := byID[id]
		if !ok {
			return nil, notFound(fmt.Sprintf("wallet %s not found", id))
		}
		if wl.Status != models.WalletStatusActive {
			return nil, invalid(fmt.Sprintf("wallet %s is %s", id, wl.Status))
		}
	}
	return byID, nil
}

func walletToDto(w models.Wallet) types.WalletResDto {
	exp := 2
	if c, ok := currency.Lookup(w.Currency); ok {
		exp = c.Exponent
	}
	dto := types.WalletResDto{
		ID:                w.ID,
		CreatedAt:         w.CreatedAt,
		UpdatedAt:         w.UpdatedAt,
		Number:            w.Number,
		Status:            w.Status,
		Currency:          w.Currency,
		AvailableBalance:  types.MoneyFromMinor(w.AvailableBalance, exp),
		ProcessingBalance: types.MoneyFromMinor(w.ProcessingBalance, exp),
		ActualBalance:     types.MoneyFromMinor(w.ActualBalance, exp),
		Version:           w.Version,
		Profile:           types.ProfileDto{ID: w.Profile.ID, FullName: w.Profile.FullName},
	}
	if w.Merchant != nil {
		dto.Merchant = types.ProfileDto{ID: w.Merchant.ID, FullName: w.Merchant.FullName}
	}
	return dto
}
