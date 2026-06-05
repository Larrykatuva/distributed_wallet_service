package services

import (
	"errors"

	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

type ProfileInterface interface {
	Filter(filters models.Profile) (*models.Profile, error)
	Register(payload types.ProfileReqDto) (types.ProfileResDto, error)
}

type ProfileServiceImpl struct {
	db *gorm.DB
}

func NewProfileService(db *gorm.DB) *ProfileServiceImpl {
	return &ProfileServiceImpl{
		db: db,
	}
}

func (p *ProfileServiceImpl) Filter(filters models.Profile) (*models.Profile, error) {
	var profile models.Profile

	result := p.db.Where(filters).First(&profile)
	if result.RowsAffected == 0 {
		return nil, nil
	}

	if result.Error != nil {
		return nil, result.Error
	}

	return &profile, nil
}

func (p *ProfileServiceImpl) Register(payload types.ProfileReqDto) (types.ProfileResDto, error) {
	exists, err := p.Filter(models.Profile{Email: payload.Email})
	if err != nil {
		return types.ProfileResDto{}, err
	}

	if exists != nil {
		return types.ProfileResDto{}, errors.New("profile with given email already exists")
	}

	profile := models.Profile{
		ExternalID: payload.ExternalId,
		Type:       payload.Type,
		FullName:   payload.FullName,
		Email:      payload.Email,
		Phone:      payload.Phone,
		Metadata:   payload.Metadata,
	}

	if err = p.db.Create(&profile).Error; err != nil {
		return types.ProfileResDto{}, err
	}

	return types.ProfileResDto{
		ProfileReqDto: payload,
		ID:            profile.ID,
		CreatedAt:     profile.CreatedAt,
		UpdatedAt:     profile.UpdatedAt,
	}, nil
}
