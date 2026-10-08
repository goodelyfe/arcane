// Package secretsource holds the API contracts for external secret managers
// (Infisical) and the per-project bindings that pull secrets at deploy time.
package secretsource

import "time"

// ProviderInfisical is the only provider supported today.
const ProviderInfisical = "infisical"

// Source is a saved connection to an external secret manager. The client
// secret is write-only and never returned.
type Source struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Provider         string     `json:"provider"`
	SiteURL          string     `json:"siteUrl"`
	ClientID         string     `json:"clientId"`
	OrganizationSlug string     `json:"organizationSlug,omitempty"`
	HasClientSecret  bool       `json:"hasClientSecret"`
	BindingCount     int        `json:"bindingCount"`
	LastTestedAt     *time.Time `json:"lastTestedAt,omitempty"`
	LastTestError    *string    `json:"lastTestError,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        *time.Time `json:"updatedAt,omitempty"`
}

type CreateSourceRequest struct {
	Name             string `json:"name"`
	SiteURL          string `json:"siteUrl,omitempty"`
	ClientID         string `json:"clientId"`
	ClientSecret     string `json:"clientSecret"`
	OrganizationSlug string `json:"organizationSlug,omitempty"`
}

// UpdateSourceRequest updates a source; nil fields keep the current value. A
// nil or empty ClientSecret keeps the stored secret.
type UpdateSourceRequest struct {
	Name             *string `json:"name,omitzero"`
	SiteURL          *string `json:"siteUrl,omitzero"`
	ClientID         *string `json:"clientId,omitzero"`
	ClientSecret     *string `json:"clientSecret,omitzero"`
	OrganizationSlug *string `json:"organizationSlug,omitzero"`
}

// TestSourceRequest tests connection settings without saving them. When
// SourceID is set and ClientSecret is empty, the stored secret is used and the
// result is recorded on that source.
type TestSourceRequest struct {
	SourceID         string `json:"sourceId,omitempty"`
	SiteURL          string `json:"siteUrl,omitempty"`
	ClientID         string `json:"clientId"`
	ClientSecret     string `json:"clientSecret,omitempty"`
	OrganizationSlug string `json:"organizationSlug,omitempty"`
}

type TestSourceResult struct {
	OK              bool   `json:"ok"`
	Message         string `json:"message"`
	TokenTTLSeconds int64  `json:"tokenTtlSeconds,omitempty"`
	ProjectsVisible int    `json:"projectsVisible"`
	CanListProjects bool   `json:"canListProjects"`
}

type RemoteEnvironment struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// RemoteProject is a project in the secret manager, used to fill pickers.
type RemoteProject struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Slug         string              `json:"slug"`
	Environments []RemoteEnvironment `json:"environments"`
}

// Binding connects one Arcane project to one secret path.
type Binding struct {
	ProjectID        string     `json:"projectId"`
	SourceID         string     `json:"sourceId"`
	SourceName       string     `json:"sourceName"`
	RemoteProjectID  string     `json:"remoteProjectId"`
	Environment      string     `json:"environment"`
	SecretPath       string     `json:"secretPath"`
	IncludeImports   bool       `json:"includeImports"`
	ExpandReferences bool       `json:"expandReferences"`
	Required         bool       `json:"required"`
	Enabled          bool       `json:"enabled"`
	DeployedAt       *time.Time `json:"deployedAt,omitempty"`
	LastFetchedAt    *time.Time `json:"lastFetchedAt,omitempty"`
	LastFetchError   *string    `json:"lastFetchError,omitempty"`
}

type UpsertBindingRequest struct {
	SourceID         string `json:"sourceId"`
	RemoteProjectID  string `json:"remoteProjectId"`
	Environment      string `json:"environment"`
	SecretPath       string `json:"secretPath,omitempty"`
	IncludeImports   bool   `json:"includeImports"`
	ExpandReferences bool   `json:"expandReferences"`
	Required         bool   `json:"required"`
	Enabled          bool   `json:"enabled"`
}

// CheckResult reports what a fetch would deliver, without any secret values.
type CheckResult struct {
	Keys []string `json:"keys"`
	// OverriddenKeys are keys also defined in the project's .env or
	// .env.global; the Infisical value wins at deploy.
	OverriddenKeys []string `json:"overriddenKeys"`
	// InvalidKeys cannot be used as environment variable names and are skipped.
	InvalidKeys []string `json:"invalidKeys"`
	// RedeployNeeded is true when the secrets changed since the last deploy.
	RedeployNeeded bool       `json:"redeployNeeded"`
	NeverDeployed  bool       `json:"neverDeployed"`
	FetchedAt      time.Time  `json:"fetchedAt"`
	DeployedAt     *time.Time `json:"deployedAt,omitempty"`
}
