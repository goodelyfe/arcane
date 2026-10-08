package secretsource

import (
	"time"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"

	"github.com/getarcaneapp/arcane/backend/v2/internal/database"
)

// SecretSource is a saved connection to an external secret provider.
// Credential (the deploy identity's secret) and SetupCredential (the optional
// setup identity's secret) hold ciphertext; Settings never holds secrets.
type SecretSource struct {
	database.BaseModel

	Name            string                           `gorm:"column:name"`
	Provider        string                           `gorm:"column:provider"`
	Settings        secretsourcetypes.SourceSettings `gorm:"column:settings;serializer:json"`
	Credential      string                           `gorm:"column:credential"`
	SetupCredential string                           `gorm:"column:setup_credential"`
	LastTestedAt    *time.Time                       `gorm:"column:last_tested_at"`
	LastTestError   *string                          `gorm:"column:last_test_error"`
}

func (SecretSource) TableName() string { return "secret_sources" }

// ProjectSecretBinding connects one project to one provider target. It stores
// no secret values. The hashes are salted HMACs of a whole secret set: what the
// last deploy used, what the last check saw, and the last change already
// announced (so a change is reported and auto-redeployed only once).
type ProjectSecretBinding struct {
	database.BaseModel

	ProjectID        string                          `gorm:"column:project_id"`
	SourceID         string                          `gorm:"column:source_id"`
	Source           *SecretSource                   `gorm:"foreignKey:SourceID"`
	Target           secretsourcetypes.BindingTarget `gorm:"column:target;serializer:json"`
	Required         bool                            `gorm:"column:required"`
	Enabled          bool                            `gorm:"column:enabled"`
	AutoRedeploy     bool                            `gorm:"column:auto_redeploy"`
	HashSalt         string                          `gorm:"column:hash_salt"`
	DeployedHash     *string                         `gorm:"column:deployed_hash"`
	DeployedAt       *time.Time                      `gorm:"column:deployed_at"`
	LastSeenHash     *string                         `gorm:"column:last_seen_hash"`
	LastNotifiedHash *string                         `gorm:"column:last_notified_hash"`
	LastCheckedAt    *time.Time                      `gorm:"column:last_checked_at"`
	LastFetchedAt    *time.Time                      `gorm:"column:last_fetched_at"`
	LastFetchError   *string                         `gorm:"column:last_fetch_error"`
}

func (ProjectSecretBinding) TableName() string { return "project_secret_bindings" }

// redeployNeededInternal reports whether the last check saw a different secret
// set than the last deploy used.
func (b *ProjectSecretBinding) redeployNeededInternal() bool {
	return b.DeployedHash != nil && b.LastSeenHash != nil && *b.LastSeenHash != *b.DeployedHash
}
