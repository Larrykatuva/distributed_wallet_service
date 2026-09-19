package services

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/katuva/wallet/dpk/cache"
	"github.com/katuva/wallet/internal/models"
	"github.com/katuva/wallet/internal/types"
	"gorm.io/gorm"
)

type ProfileService struct {
	db *gorm.DB
}

func NewProfileService(db *gorm.DB) *ProfileService {
	return &ProfileService{db: db}
}

// profileTTL is generous: profiles are immutable after registration.
const profileTTL = 30 * time.Minute

// FindByID returns nil, nil when the profile does not exist. Hits are served
// from the cache; misses are loaded and cached.
func (p *ProfileService) FindByID(id uuid.UUID) (*models.Profile, error) {
	var cached models.Profile
	if cache.Get(cache.ProfileKey(id.String()), &cached) {
		return &cached, nil
	}
	var profile models.Profile
	err := p.db.First(&profile, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find profile: %w", err)
	}
	cacheProfile(profile)
	return &profile, nil
}

func cacheProfile(pr models.Profile) {
	cache.Set(cache.ProfileKey(pr.ID.String()), pr, profileTTL)
	cache.Set(cache.ProfileUsernameKey(strings.ToLower(pr.Email)), pr, profileTTL)
	if pr.Phone != nil && *pr.Phone != "" {
		cache.Set(cache.ProfileUsernameKey(*pr.Phone), pr, profileTTL)
	}
}

// FindByIDs loads several profiles keyed by ID.
func (p *ProfileService) FindByIDs(ids []uuid.UUID) (map[uuid.UUID]models.Profile, error) {
	var profiles []models.Profile
	if err := p.db.Where("id IN ?", ids).Find(&profiles).Error; err != nil {
		return nil, fmt.Errorf("find profiles: %w", err)
	}
	out := make(map[uuid.UUID]models.Profile, len(profiles))
	for _, pr := range profiles {
		out[pr.ID] = pr
	}
	return out, nil
}

// Get returns a profile by ID as a DTO.
func (p *ProfileService) Get(id uuid.UUID) (types.ProfileResDto, error) {
	profile, err := p.FindByID(id)
	if err != nil {
		return types.ProfileResDto{}, err
	}
	if profile == nil {
		return types.ProfileResDto{}, notFound("profile not found")
	}
	return profileToDto(*profile), nil
}

// GetByUsername looks a profile up by email or phone (exact, case-insensitive
// for email).
func (p *ProfileService) GetByUsername(username string) (types.ProfileResDto, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return types.ProfileResDto{}, invalid("username is required")
	}
	var profile models.Profile
	if cache.Get(cache.ProfileUsernameKey(strings.ToLower(username)), &profile) {
		return profileToDto(profile), nil
	}
	err := p.db.Where("LOWER(email) = LOWER(?) OR phone = ?", username, username).First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return types.ProfileResDto{}, notFound("profile not found")
	}
	if err != nil {
		return types.ProfileResDto{}, fmt.Errorf("find profile: %w", err)
	}
	cacheProfile(profile)
	return profileToDto(profile), nil
}

// List returns profiles newest first, filtered and paginated.
func (p *ProfileService) List(f types.ProfileFilter, page types.Pagination) (types.Page[types.ProfileResDto], error) {
	page = page.Normalize()
	q := p.db.Model(&models.Profile{})
	if f.ExternalID != nil {
		q = q.Where("external_id = ?", *f.ExternalID)
	}
	if f.Type != nil {
		q = q.Where("type = ?", *f.Type)
	}
	if f.Search != nil {
		like := "%" + strings.ToLower(*f.Search) + "%"
		q = q.Where("LOWER(full_name) LIKE ? OR LOWER(email) LIKE ? OR phone LIKE ?", like, like, like)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return types.Page[types.ProfileResDto]{}, fmt.Errorf("count profiles: %w", err)
	}
	var rows []models.Profile
	err := q.Order("created_at DESC").Offset(page.Offset()).Limit(page.PageSize).Find(&rows).Error
	if err != nil {
		return types.Page[types.ProfileResDto]{}, fmt.Errorf("list profiles: %w", err)
	}
	out := make([]types.ProfileResDto, 0, len(rows))
	for _, r := range rows {
		out = append(out, profileToDto(r))
	}
	return types.NewPage(out, page, total), nil
}

func profileToDto(m models.Profile) types.ProfileResDto {
	return types.ProfileResDto{
		ProfileReqDto: types.ProfileReqDto{
			ExternalId: m.ExternalID, Type: m.Type, FullName: m.FullName,
			Email: m.Email, Phone: m.Phone, Metadata: m.Metadata,
		},
		ID: m.ID, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

func (p *ProfileService) Register(payload types.ProfileReqDto) (types.ProfileResDto, error) {
	var count int64
	err := p.db.Model(&models.Profile{}).
		Where("email = ? OR external_id = ?", payload.Email, payload.ExternalId).
		Count(&count).Error
	if err != nil {
		return types.ProfileResDto{}, fmt.Errorf("check profile: %w", err)
	}
	if count > 0 {
		return types.ProfileResDto{}, conflict("profile with given email or external_id already exists")
	}

	metadata := payload.Metadata
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}

	profile := models.Profile{
		ExternalID: payload.ExternalId,
		Type:       payload.Type,
		FullName:   payload.FullName,
		Email:      payload.Email,
		Phone:      payload.Phone,
		Metadata:   metadata,
	}

	if err = p.db.Create(&profile).Error; err != nil {
		return types.ProfileResDto{}, fmt.Errorf("create profile: %w", err)
	}
	cacheProfile(profile)

	return types.ProfileResDto{
		ProfileReqDto: payload,
		ID:            profile.ID,
		CreatedAt:     profile.CreatedAt,
		UpdatedAt:     profile.UpdatedAt,
	}, nil
}
