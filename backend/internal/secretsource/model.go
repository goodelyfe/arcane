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

// ProjectSecretBinding connects a project to one provider target; a project
// can have several, applied in Position order. It stores no secret values:
// the hashes are salted HMACs of one binding's secret set (what the last
// deploy used, what the last check saw, and the last change already announced,
// so a change is reported and auto-redeployed only once), and DeployedKeys
// are the names the last deploy delivered.
//
// The table is secret_bindings, with an owner kind, so standalone containers
// can be bound later without another table; only projects are supported now.
type ProjectSecretBinding struct {
	database.BaseModel

	OwnerKind        string                          `gorm:"column:owner_kind"`
	ProjectID        string                          `gorm:"column:project_id"`
	Position         int                             `gorm:"column:position"`
	SourceID         string                          `gorm:"column:source_id"`
	Source           *SecretSource                   `gorm:"foreignKey:SourceID"`
	Target           secretsourcetypes.BindingTarget `gorm:"column:target;serializer:json"`
	Required         bool                            `gorm:"column:required"`
	Enabled          bool                            `gorm:"column:enabled"`
	AutoRedeploy     bool                            `gorm:"column:auto_redeploy"`
	HashSalt         string                          `gorm:"column:hash_salt"`
	DeployedHash     *string                         `gorm:"column:deployed_hash"`
	DeployedKeys     []string                        `gorm:"column:deployed_keys;serializer:json"`
	DeployedAt       *time.Time                      `gorm:"column:deployed_at"`
	LastSeenHash     *string                         `gorm:"column:last_seen_hash"`
	LastNotifiedHash *string                         `gorm:"column:last_notified_hash"`
	LastCheckedAt    *time.Time                      `gorm:"column:last_checked_at"`
	LastFetchedAt    *time.Time                      `gorm:"column:last_fetched_at"`
	LastFetchError   *string                         `gorm:"column:last_fetch_error"`
}

func (ProjectSecretBinding) TableName() string { return "secret_bindings" }

// redeployNeededInternal reports whether the last check saw a different secret
// set than the last deploy used.
func (b *ProjectSecretBinding) redeployNeededInternal() bool {
	return b.DeployedHash != nil && b.LastSeenHash != nil && *b.LastSeenHash != *b.DeployedHash
}
