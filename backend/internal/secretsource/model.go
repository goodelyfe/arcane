package secretsource

import (
	"time"

	"github.com/getarcaneapp/arcane/backend/v2/internal/database"
)

// SecretSource is a saved connection to an external secret manager.
// ClientSecret holds ciphertext.
type SecretSource struct {
	database.BaseModel

	Name             string     `gorm:"column:name"`
	Provider         string     `gorm:"column:provider"`
	SiteURL          string     `gorm:"column:site_url"`
	ClientID         string     `gorm:"column:client_id"`
	ClientSecret     string     `gorm:"column:client_secret"`
	OrganizationSlug string     `gorm:"column:organization_slug"`
	LastTestedAt     *time.Time `gorm:"column:last_tested_at"`
	LastTestError    *string    `gorm:"column:last_test_error"`
}

func (SecretSource) TableName() string { return "secret_sources" }

// ProjectSecretBinding connects one project to one secret path. It stores no
// secret values: DeployedHash is a salted HMAC of the set delivered at the last
// deploy, used only to tell whether the secrets changed since.
type ProjectSecretBinding struct {
	database.BaseModel

	ProjectID        string        `gorm:"column:project_id"`
	SourceID         string        `gorm:"column:source_id"`
	Source           *SecretSource `gorm:"foreignKey:SourceID"`
	RemoteProjectID  string        `gorm:"column:remote_project_id"`
	Environment      string        `gorm:"column:environment"`
	SecretPath       string        `gorm:"column:secret_path"`
	IncludeImports   bool          `gorm:"column:include_imports"`
	ExpandReferences bool          `gorm:"column:expand_references"`
	Required         bool          `gorm:"column:required"`
	Enabled          bool          `gorm:"column:enabled"`
	HashSalt         string        `gorm:"column:hash_salt"`
	DeployedHash     *string       `gorm:"column:deployed_hash"`
	DeployedAt       *time.Time    `gorm:"column:deployed_at"`
	LastFetchedAt    *time.Time    `gorm:"column:last_fetched_at"`
	LastFetchError   *string       `gorm:"column:last_fetch_error"`
}

func (ProjectSecretBinding) TableName() string { return "project_secret_bindings" }
