// Package secretsource holds the API contracts for external secret providers
// and the per-project bindings that pull secrets at deploy time.
package secretsource

import "time"

// Supported providers.
const (
	ProviderInfisical   = "infisical"
	ProviderBitwarden   = "bitwarden"
	ProviderVault       = "vault"
	ProviderDoppler     = "doppler"
	ProviderOnePassword = "onepassword"
	ProviderHTTP        = "http"
)

// Providers lists every supported provider.
var Providers = []string{ProviderInfisical, ProviderBitwarden, ProviderVault, ProviderDoppler, ProviderOnePassword, ProviderHTTP}

// 1Password binding scopes.
const (
	// OnePasswordScopeVault maps every item in a vault: item title is the key,
	// its password (or credential, or notes) is the value.
	OnePasswordScopeVault = "vault"
	// OnePasswordScopeItem maps the fields of one item: label is the key.
	OnePasswordScopeItem = "item"
)

// Vault and OpenBao authentication methods.
const (
	VaultAuthToken   = "token"
	VaultAuthAppRole = "approle"
)

// Bitwarden binding scopes.
const (
	// BitwardenScopeFolder maps every item in a folder: item name is the key,
	// the login password (or a secure note's text) is the value.
	BitwardenScopeFolder = "folder"
	// BitwardenScopeCollection does the same for an organization collection.
	BitwardenScopeCollection = "collection"
	// BitwardenScopeItem maps the custom fields of one item to keys and values.
	BitwardenScopeItem = "item"
)

// InfisicalSettings connects to Infisical with a Universal Auth machine
// identity. The client secret is the source's credential.
//
// SetupClientID optionally names a second identity that the project setup
// wizard uses to create projects, folders, and secrets. Its client secret is
// the source's setup credential. Deploys never use it.
type InfisicalSettings struct {
	SiteURL          string `json:"siteUrl"`
	ClientID         string `json:"clientId"`
	OrganizationSlug string `json:"organizationSlug,omitempty"`
	SetupClientID    string `json:"setupClientId,omitempty"`
}

// BitwardenSettings points at a Bitwarden CLI `bw serve` endpoint, which works
// with both Bitwarden and Vaultwarden. The CLI holds the vault session, so this
// provider has no credential of its own.
type BitwardenSettings struct {
	ServeURL string `json:"serveUrl"`
}

// VaultSettings connects to HashiCorp Vault or OpenBao. The credential is
// the token (token auth) or the AppRole secret ID.
type VaultSettings struct {
	Address string `json:"address"`
	// Namespace is optional (Vault Enterprise and OpenBao namespaces).
	Namespace  string `json:"namespace,omitempty"`
	AuthMethod string `json:"authMethod"`
	// RoleID and AppRoleMount are used with AppRole auth.
	RoleID       string `json:"roleId,omitempty"`
	AppRoleMount string `json:"appRoleMount,omitempty"`
	// SetupToken reports that a separate token for the project setup wizard
	// is stored as the source's setup credential. Deploys never use it, so
	// the deploy token can stay read-only.
	SetupToken bool `json:"setupToken,omitempty"`
}

// DopplerSettings connects to Doppler. The credential is a service token
// (scoped to one config) or a personal or service-account token.
type DopplerSettings struct {
	// APIURL is optional; empty means https://api.doppler.com.
	APIURL string `json:"apiUrl,omitempty"`
}

// OnePasswordSettings points at a 1Password Connect server. The credential is
// the Connect access token.
type OnePasswordSettings struct {
	ServerURL string `json:"serverUrl"`
}

// HTTPSettings points at any endpoint that answers GET with a flat JSON
// object of names and values. The credential, when set, is sent as a bearer
// token.
type HTTPSettings struct {
	BaseURL string `json:"baseUrl"`
}

// SourceSettings carries the non-secret settings of exactly one provider.
type SourceSettings struct {
	Infisical   *InfisicalSettings   `json:"infisical,omitempty"`
	Bitwarden   *BitwardenSettings   `json:"bitwarden,omitempty"`
	Vault       *VaultSettings       `json:"vault,omitempty"`
	Doppler     *DopplerSettings     `json:"doppler,omitempty"`
	OnePassword *OnePasswordSettings `json:"onepassword,omitempty"`
	HTTP        *HTTPSettings        `json:"http,omitempty"`
}

// Source is a saved connection to an external secret provider. The
// credential is write-only and never returned.
type Source struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Provider      string         `json:"provider"`
	Settings      SourceSettings `json:"settings"`
	HasCredential bool           `json:"hasCredential"`
	// HasSetupCredential reports whether the setup wizard can write to the
	// provider.
	HasSetupCredential bool       `json:"hasSetupCredential"`
	BindingCount       int        `json:"bindingCount"`
	LastTestedAt       *time.Time `json:"lastTestedAt,omitempty"`
	LastTestError      *string    `json:"lastTestError,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          *time.Time `json:"updatedAt,omitempty"`
}

type CreateSourceRequest struct {
	Name       string         `json:"name"`
	Provider   string         `json:"provider"`
	Settings   SourceSettings `json:"settings"`
	Credential string         `json:"credential,omitempty"`
	// SetupCredential is the client secret of InfisicalSettings.SetupClientID.
	SetupCredential string `json:"setupCredential,omitempty"`
}

// UpdateSourceRequest updates a source; nil fields keep the current value. A
// nil or empty Credential or SetupCredential keeps the stored one. Clearing
// the setup client ID also deletes the stored setup credential. The provider
// cannot change.
type UpdateSourceRequest struct {
	Name            *string         `json:"name,omitzero"`
	Settings        *SourceSettings `json:"settings,omitzero"`
	Credential      *string         `json:"credential,omitzero"`
	SetupCredential *string         `json:"setupCredential,omitzero"`
	// ClearCredential removes the stored credential. Only HTTP sources,
	// whose bearer token is optional, accept it.
	ClearCredential bool `json:"clearCredential,omitempty"`
}

// TestSourceRequest tests settings without saving them. When SourceID is set
// and Credential is empty, the stored credential is used, and the result is
// recorded on the source when the tested settings match the stored ones.
type TestSourceRequest struct {
	SourceID   string         `json:"sourceId,omitempty"`
	Provider   string         `json:"provider"`
	Settings   SourceSettings `json:"settings"`
	Credential string         `json:"credential,omitempty"`
}

type TestSourceResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	// CanBrowse reports whether the pickers can list targets; when false the
	// UI asks for IDs by hand.
	CanBrowse    bool `json:"canBrowse"`
	VisibleCount int  `json:"visibleCount"`
}

// Browse kinds per provider.
const (
	BrowseInfisicalProjects    = "projects"
	BrowseInfisicalFolders     = "folders"
	BrowseBitwardenFolders     = "folders"
	BrowseBitwardenCollections = "collections"
	BrowseBitwardenItems       = "items"
	BrowseVaultMounts          = "mounts"
	BrowseVaultPaths           = "paths"
	BrowseDopplerProjects      = "projects"
	BrowseDopplerConfigs       = "configs"
	BrowseOnePasswordVaults    = "vaults"
	BrowseOnePasswordItems     = "items"
)

// BrowseQuery selects what to list from a source for the binding pickers.
type BrowseQuery struct {
	Kind        string `json:"kind"`
	ProjectID   string `json:"projectId,omitempty"`
	Environment string `json:"environment,omitempty"`
	Path        string `json:"path,omitempty"`
	// Mount and KVVersion select the Vault/OpenBao mount for "paths".
	Mount     string `json:"mount,omitempty"`
	KVVersion int    `json:"kvVersion,omitempty"`
	// VaultID selects the 1Password vault for "items".
	VaultID string `json:"vaultId,omitempty"`
}

// BrowseItem is one pickable target. Options carries nested choices, such as
// the environments of an Infisical project.
type BrowseItem struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Detail  string       `json:"detail,omitempty"`
	Options []BrowseItem `json:"options,omitempty"`
}

// InfisicalTarget selects one environment path of one Infisical project.
type InfisicalTarget struct {
	ProjectID        string `json:"projectId"`
	Environment      string `json:"environment"`
	SecretPath       string `json:"secretPath"`
	IncludeImports   bool   `json:"includeImports"`
	ExpandReferences bool   `json:"expandReferences"`
}

// BitwardenTarget selects a folder, a collection, or a single item.
type BitwardenTarget struct {
	Scope string `json:"scope"`
	ID    string `json:"id"`
	// Name is the folder, collection, or item name at bind time, for display.
	Name string `json:"name,omitempty"`
}

// VaultTarget selects one secret in a KV mount. Each key of the secret is
// one variable.
type VaultTarget struct {
	Mount string `json:"mount"`
	Path  string `json:"path"`
	// KVVersion is 1 or 2.
	KVVersion int `json:"kvVersion"`
}

// DopplerTarget selects one config. Both fields are empty with a service
// token, which is already scoped to one config.
type DopplerTarget struct {
	Project string `json:"project,omitempty"`
	Config  string `json:"config,omitempty"`
}

// OnePasswordTarget selects a vault or one item in a vault.
type OnePasswordTarget struct {
	Scope   string `json:"scope"`
	VaultID string `json:"vaultId"`
	ItemID  string `json:"itemId,omitempty"`
	// Name is the vault or item name at bind time, for display.
	Name string `json:"name,omitempty"`
}

// HTTPTarget selects a path under the endpoint's base URL. Empty reads the
// base URL itself.
type HTTPTarget struct {
	Path string `json:"path,omitempty"`
}

// BindingTarget carries the target of exactly one provider.
type BindingTarget struct {
	Infisical   *InfisicalTarget   `json:"infisical,omitempty"`
	Bitwarden   *BitwardenTarget   `json:"bitwarden,omitempty"`
	Vault       *VaultTarget       `json:"vault,omitempty"`
	Doppler     *DopplerTarget     `json:"doppler,omitempty"`
	OnePassword *OnePasswordTarget `json:"onepassword,omitempty"`
	HTTP        *HTTPTarget        `json:"http,omitempty"`
}

// Binding connects one Arcane project to one target in a secret source.
type Binding struct {
	ProjectID      string        `json:"projectId"`
	SourceID       string        `json:"sourceId"`
	SourceName     string        `json:"sourceName"`
	Provider       string        `json:"provider"`
	Target         BindingTarget `json:"target"`
	Required       bool          `json:"required"`
	Enabled        bool          `json:"enabled"`
	AutoRedeploy   bool          `json:"autoRedeploy"`
	RedeployNeeded bool          `json:"redeployNeeded"`
	DeployedAt     *time.Time    `json:"deployedAt,omitempty"`
	LastFetchedAt  *time.Time    `json:"lastFetchedAt,omitempty"`
	LastCheckedAt  *time.Time    `json:"lastCheckedAt,omitempty"`
	LastFetchError *string       `json:"lastFetchError,omitempty"`
}

type UpsertBindingRequest struct {
	SourceID     string        `json:"sourceId"`
	Target       BindingTarget `json:"target"`
	Required     bool          `json:"required"`
	Enabled      bool          `json:"enabled"`
	AutoRedeploy bool          `json:"autoRedeploy"`
}

// CheckResult reports what a fetch would deliver, without any secret values.
type CheckResult struct {
	Keys []string `json:"keys"`
	// OverriddenKeys are keys also defined in the project's .env or
	// .env.global; the provider's value wins at deploy.
	OverriddenKeys []string `json:"overriddenKeys"`
	// InvalidKeys cannot be used as environment variable names, or have no
	// usable value, and are skipped.
	InvalidKeys []string `json:"invalidKeys"`
	// RedeployNeeded is true when the secrets changed since the last deploy.
	RedeployNeeded bool       `json:"redeployNeeded"`
	NeverDeployed  bool       `json:"neverDeployed"`
	FetchedAt      time.Time  `json:"fetchedAt"`
	DeployedAt     *time.Time `json:"deployedAt,omitempty"`
}
