package services

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

type WalletInterface interface {
	Filter(filters models.Wallet) (*models.Wallet, error)
	Create(payload types.WalletReqDto) (types.WalletResDto, error)
	FilterByIDs(ids []uuid.UUID) ([]models.Wallet, error)
	ValidateWallets(walletIds []uuid.UUID) ([]models.Wallet, error)
}

type WalletServiceImpl struct {
	db             *gorm.DB
	profileService *ProfileServiceImpl
}

func NewWalletService(db *gorm.DB) *WalletServiceImpl {
	return &WalletServiceImpl{
		db:             db,
		profileService: NewProfileService(db),
	}
}

func (w *WalletServiceImpl) Filter(filters models.Wallet) (*models.Wallet, error) {
	var wallet models.Wallet

	result := w.db.Where(filters).First(&wallet)
	if result.RowsAffected == 0 {
		return nil, nil
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return &wallet, nil
}

func (w *WalletServiceImpl) Create(payload types.WalletReqDto) (types.WalletResDto, error) {
	type profileResult struct {
		profile *models.Profile
		err     error
	}

	profileCh := make(chan profileResult, 1)
	merchantCh := make(chan profileResult, 1)

	// Fetch profile and merchant concurrently
	go func() {
		p, err := w.profileService.Filter(models.Profile{ID: payload.ProfileID})
		profileCh <- profileResult{p, err}
	}()

	go func() {
		m, err := w.profileService.Filter(models.Profile{ID: payload.MerchantID})
		merchantCh <- profileResult{m, err}
	}()

	profileRes := <-profileCh
	merchantRes := <-merchantCh

	if profileRes.err != nil {
		return types.WalletResDto{}, profileRes.err
	}
	if profileRes.profile == nil {
		return types.WalletResDto{}, errors.New("profile not found")
	}

	if merchantRes.err != nil {
		return types.WalletResDto{}, merchantRes.err
	}
	if merchantRes.profile == nil {
		return types.WalletResDto{}, errors.New("merchant not found")
	}

	profile := profileRes.profile
	merchant := merchantRes.profile

	wallet := models.Wallet{
		Currency:   payload.Currency,
		ProfileID:  profile.ID,
		MerchantID: &merchant.ID,
	}

	if err := w.db.Omit("number").Create(&wallet).Error; err != nil {
		return types.WalletResDto{}, err
	}

	newWallet, _ := w.Filter(models.Wallet{ID: wallet.ID})

	return types.WalletResDto{
		ID:                newWallet.ID,
		CreatedAt:         newWallet.CreatedAt,
		UpdatedAt:         newWallet.UpdatedAt,
		Number:            newWallet.Number,
		Status:            newWallet.Status,
		Currency:          newWallet.Currency,
		AvailableBalance:  newWallet.AvailableBalance,
		ProcessingBalance: newWallet.ProcessingBalance,
		ActualBalance:     newWallet.ActualBalance,
		Version:           newWallet.Version,
		Profile: types.ProfileDto{
			ID:       profile.ID,
			FullName: profile.FullName,
		},
		Merchant: types.ProfileDto{
			ID:       merchant.ID,
			FullName: merchant.FullName,
		},
	}, nil
}

func (w *WalletServiceImpl) FilterByIDs(ids []uuid.UUID) ([]models.Wallet, error) {
	var wallets []models.Wallet
	err := w.db.Where("id IN ?", ids).Preload("Profile").Preload("Merchant").Find(&wallets).Error
	return wallets, err
}

func (w *WalletServiceImpl) ValidateWallets(walletIds []uuid.UUID) ([]models.Wallet, error) {
	if len(walletIds) == 0 {
		return nil, errors.New("all wallets not found")
	}

	wallets, err := w.FilterByIDs(walletIds)
	if err != nil {
		return nil, err
	}

	walletMap := make(map[uuid.UUID]models.Wallet, len(wallets))
	for _, wallet := range wallets {
		walletMap[wallet.ID] = wallet
	}

	result := make([]models.Wallet, 0, len(walletIds))
	for _, id := range walletIds {
		wallet, ok := walletMap[id]
		if !ok {
			return nil, fmt.Errorf("wallet id %s not found", id)
		}
		if wallet.Status != models.WalletStatusActive {
			return nil, fmt.Errorf("wallet %s is already %s", id, wallet.Status)
		}
		result = append(result, wallet)
	}

	return result, nil
}
