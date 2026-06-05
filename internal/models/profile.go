package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	_ "gorm.io/gorm"
)

type ProfileType string

const (
	ProfileTypeIndividual ProfileType = "individual"
	ProfileTypeBusiness   ProfileType = "business"
)

type Profile struct {
	ID         uuid.UUID       `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	ExternalID string          `gorm:"type:text;uniqueIndex;not null"`
	Type       ProfileType     `gorm:"type:profile_type;not null;default:individual"`
	FullName   string          `gorm:"type:text"`
	Email      string          `gorm:"type:text;uniqueIndex;not null"`
	Phone      *string         `gorm:"type:text"`
	Metadata   json.RawMessage `gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt  time.Time       `gorm:"not null"`
	UpdatedAt  time.Time       `gorm:"not null"`
}

func (Profile) TableName() string {
	return "profiles"
}
