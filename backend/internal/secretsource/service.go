package secretsource

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"
	usertypes "github.com/getarcaneapp/arcane/types/v2/user"
	"go.getarcane.app/sys/crypto"
	"gorm.io/gorm"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/internal/database"
	"github.com/getarcaneapp/arcane/backend/v2/internal/event"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/infisical"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/projects"
)

const (
	fetchTimeout       = 20 * time.Second
	localEnvironmentID = "0"
)

// envKeyPattern matches names that compose can interpolate and pass through.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ProjectRef identifies the project a binding belongs to. Path and
// ProjectsDirectory are only needed to report .env keys that a secret overrides.
type ProjectRef struct {
	ID                string
	Name              string
	Path              string
	ProjectsDirectory string
}

// ProjectResolver looks up a project of the local environment.
type ProjectResolver func(ctx context.Context, projectID string) (ProjectRef, error)

// DeployEnv is the secret set resolved for one deploy. Hash identifies the set
// without revealing it and is recorded with RecordDeployed once the deploy
// succeeds.
type DeployEnv struct {
	Values map[string]string
	Hash   string
}

// SecretSourceService manages secret sources, project bindings, and the
// deploy-time fetch of bound secrets. Secret values are only ever held in
// memory for the duration of a deploy or check.
type SecretSourceService struct {
	db           *database.DB
	eventService *event.EventService
	httpClient   *http.Client

	clientsMu sync.Mutex
	clients   map[string]cachedClient
}

type cachedClient struct {
	fingerprint string
	client      *infisical.Client
}

func NewSecretSourceService(db *database.DB, eventService *event.EventService) *SecretSourceService {
	return &SecretSourceService{
		db:           db,
		eventService: eventService,
		// Not the SSRF-safe client: self-hosted Infisical normally lives on a
		// private, VPN, or tailnet address, which that client rejects. Creating
		// a source requires the secret-sources:create permission.
		httpClient: &http.Client{Timeout: fetchTimeout},
		clients:    make(map[string]cachedClient),
	}
}

// ---- Sources ----

func (s *SecretSourceService) ListSources(ctx context.Context) ([]secretsourcetypes.Source, error) {
	var sources []SecretSource
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&sources).Error; err != nil {
		return nil, fmt.Errorf("failed to list secret sources: %w", err)
	}
	counts, err := s.bindingCountsInternal(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]secretsourcetypes.Source, 0, len(sources))
	for i := range sources {
		result = append(result, sourceToDTOInternal(&sources[i], counts[sources[i].ID]))
	}
	return result, nil
}

func (s *SecretSourceService) GetSource(ctx context.Context, id string) (*secretsourcetypes.Source, error) {
	source, err := s.loadSourceInternal(ctx, id)
	if err != nil {
		return nil, err
	}
	counts, err := s.bindingCountsInternal(ctx)
	if err != nil {
		return nil, err
	}
	dto := sourceToDTOInternal(source, counts[source.ID])
	return &dto, nil
}

func (s *SecretSourceService) CreateSource(ctx context.Context, req secretsourcetypes.CreateSourceRequest) (*secretsourcetypes.Source, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, common.Classify(common.ErrSecretSourceInvalid, errors.New("name is required"))
	}
	siteURL, err := normalizeSiteURLInternal(req.SiteURL)
	if err != nil {
		return nil, err
	}
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" || req.ClientSecret == "" {
		return nil, common.Classify(common.ErrSecretSourceInvalid, errors.New("client ID and client secret are required"))
	}
	if conflictErr := s.ensureNameAvailableInternal(ctx, name, ""); conflictErr != nil {
		return nil, conflictErr
	}
	encrypted, err := crypto.Encrypt(req.ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt client secret: %w", err)
	}

	source := SecretSource{
		Name:             name,
		Provider:         secretsourcetypes.ProviderInfisical,
		SiteURL:          siteURL,
		ClientID:         clientID,
		ClientSecret:     encrypted,
		OrganizationSlug: strings.TrimSpace(req.OrganizationSlug),
	}
	if createErr := s.db.WithContext(ctx).Create(&source).Error; createErr != nil {
		return nil, fmt.Errorf("failed to create secret source: %w", createErr)
	}
	dto := sourceToDTOInternal(&source, 0)
	return &dto, nil
}

func (s *SecretSourceService) UpdateSource(ctx context.Context, id string, req secretsourcetypes.UpdateSourceRequest) (*secretsourcetypes.Source, error) {
	source, err := s.loadSourceInternal(ctx, id)
	if err != nil {
		return nil, err
	}
	before := *source

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, common.Classify(common.ErrSecretSourceInvalid, errors.New("name is required"))
		}
		if conflictErr := s.ensureNameAvailableInternal(ctx, name, source.ID); conflictErr != nil {
			return nil, conflictErr
		}
		source.Name = name
	}
	if req.SiteURL != nil {
		siteURL, siteErr := normalizeSiteURLInternal(*req.SiteURL)
		if siteErr != nil {
			return nil, siteErr
		}
		source.SiteURL = siteURL
	}
	if req.ClientID != nil {
		clientID := strings.TrimSpace(*req.ClientID)
		if clientID == "" {
			return nil, common.Classify(common.ErrSecretSourceInvalid, errors.New("client ID is required"))
		}
		source.ClientID = clientID
	}
	if req.ClientSecret != nil && *req.ClientSecret != "" {
		encrypted, encryptErr := crypto.Encrypt(*req.ClientSecret)
		if encryptErr != nil {
			return nil, fmt.Errorf("failed to encrypt client secret: %w", encryptErr)
		}
		source.ClientSecret = encrypted
	}
	if req.OrganizationSlug != nil {
		source.OrganizationSlug = strings.TrimSpace(*req.OrganizationSlug)
	}
	// A test result only describes the connection it was run against.
	connectionChanged := source.SiteURL != before.SiteURL ||
		source.ClientID != before.ClientID ||
		source.ClientSecret != before.ClientSecret ||
		source.OrganizationSlug != before.OrganizationSlug
	if connectionChanged {
		source.LastTestedAt = nil
		source.LastTestError = nil
	}

	if saveErr := s.db.WithContext(ctx).Save(source).Error; saveErr != nil {
		return nil, fmt.Errorf("failed to update secret source: %w", saveErr)
	}
	s.forgetClientInternal(source.ID)
	return s.GetSource(ctx, source.ID)
}

func (s *SecretSourceService) DeleteSource(ctx context.Context, id string) error {
	source, err := s.loadSourceInternal(ctx, id)
	if err != nil {
		return err
	}
	var bound int64
	if countErr := s.db.WithContext(ctx).Model(&ProjectSecretBinding{}).Where("source_id = ?", source.ID).Count(&bound).Error; countErr != nil {
		return fmt.Errorf("failed to check secret source usage: %w", countErr)
	}
	if bound > 0 {
		return common.ErrSecretSourceInUse
	}
	if deleteErr := s.db.WithContext(ctx).Delete(&SecretSource{}, "id = ?", source.ID).Error; deleteErr != nil {
		return fmt.Errorf("failed to delete secret source: %w", deleteErr)
	}
	s.forgetClientInternal(source.ID)
	return nil
}

// TestSource logs in with the given settings and checks whether the identity
// can list projects. Connection failures are reported in the result rather
// than as an error, so the UI can show Infisical's message inline.
func (s *SecretSourceService) TestSource(ctx context.Context, req secretsourcetypes.TestSourceRequest) (secretsourcetypes.TestSourceResult, error) {
	cfg := infisical.Config{
		SiteURL:          req.SiteURL,
		ClientID:         req.ClientID,
		ClientSecret:     req.ClientSecret,
		OrganizationSlug: req.OrganizationSlug,
	}

	// Record the result on the source only when the tested settings are the
	// ones it stores, so an unsaved edit never overwrites the saved status.
	recordOn := ""
	if req.SourceID != "" {
		source, err := s.loadSourceInternal(ctx, req.SourceID)
		if err != nil {
			return secretsourcetypes.TestSourceResult{}, err
		}
		storedSecret, decryptErr := crypto.Decrypt(source.ClientSecret)
		if decryptErr != nil {
			return secretsourcetypes.TestSourceResult{}, fmt.Errorf("failed to decrypt stored client secret: %w", decryptErr)
		}
		if cfg.ClientSecret == "" {
			cfg.ClientSecret = storedSecret
		}
		if cfg.ClientSecret == storedSecret && sameConnectionInternal(source, cfg) {
			recordOn = source.ID
		}
	}

	result := s.runTestInternal(ctx, cfg)
	if recordOn != "" {
		s.recordTestInternal(ctx, recordOn, result)
	}
	return result, nil
}

func (s *SecretSourceService) runTestInternal(ctx context.Context, cfg infisical.Config) secretsourcetypes.TestSourceResult {
	client, err := infisical.NewClient(s.httpClient, cfg)
	if err != nil {
		return secretsourcetypes.TestSourceResult{OK: false, Message: err.Error()}
	}
	testCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	session, err := client.Login(testCtx)
	if err != nil {
		return secretsourcetypes.TestSourceResult{OK: false, Message: err.Error()}
	}
	result := secretsourcetypes.TestSourceResult{
		OK:              true,
		Message:         "Authenticated with Infisical",
		TokenTTLSeconds: int64(session.ExpiresIn / time.Second),
	}
	remote, err := client.ListProjects(testCtx)
	if err != nil {
		result.Message = "Authenticated, but this identity cannot list projects; enter the project ID by hand"
		return result
	}
	result.CanListProjects = true
	result.ProjectsVisible = len(remote)
	return result
}

func (s *SecretSourceService) recordTestInternal(ctx context.Context, sourceID string, result secretsourcetypes.TestSourceResult) {
	now := time.Now()
	var testError *string
	if !result.OK {
		testError = new(result.Message)
	}
	err := s.db.WithContext(ctx).Model(&SecretSource{}).Where("id = ?", sourceID).
		UpdateColumns(map[string]any{"last_tested_at": now, "last_test_error": testError}).Error
	if err != nil {
		slog.WarnContext(ctx, "failed to record secret source test result", "sourceId", sourceID, "error", err)
	}
}

// ListRemoteProjects returns the Infisical projects the source's identity can
// see, with their environments, for the binding pickers.
func (s *SecretSourceService) ListRemoteProjects(ctx context.Context, sourceID string) ([]secretsourcetypes.RemoteProject, error) {
	client, err := s.clientForSourceIDInternal(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	remote, err := client.ListProjects(listCtx)
	if err != nil {
		return nil, common.Classify(common.ErrSecretFetchFailed, err)
	}
	result := make([]secretsourcetypes.RemoteProject, 0, len(remote))
	for _, project := range remote {
		envs := make([]secretsourcetypes.RemoteEnvironment, 0, len(project.Environments))
		for _, env := range project.Environments {
			envs = append(envs, secretsourcetypes.RemoteEnvironment{Name: env.Name, Slug: env.Slug})
		}
		result = append(result, secretsourcetypes.RemoteProject{ID: project.ID, Name: project.Name, Slug: project.Slug, Environments: envs})
	}
	return result, nil
}

// ListRemoteFolders returns the folder names directly under path.
func (s *SecretSourceService) ListRemoteFolders(ctx context.Context, sourceID, remoteProjectID, environment, path string) ([]string, error) {
	if strings.TrimSpace(remoteProjectID) == "" || strings.TrimSpace(environment) == "" {
		return nil, common.Classify(common.ErrSecretBindingInvalid, errors.New("project and environment are required"))
	}
	client, err := s.clientForSourceIDInternal(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	listCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	folders, err := client.ListFolders(listCtx, remoteProjectID, environment, path)
	if err != nil {
		return nil, common.Classify(common.ErrSecretFetchFailed, err)
	}
	slices.Sort(folders)
	return folders, nil
}

// ---- Bindings ----

// GetBinding returns the project's binding, or nil when it has none.
func (s *SecretSourceService) GetBinding(ctx context.Context, projectID string) (*secretsourcetypes.Binding, error) {
	binding, err := s.loadBindingInternal(ctx, projectID)
	if errors.Is(err, common.ErrSecretBindingNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dto := bindingToDTOInternal(binding)
	return &dto, nil
}

func (s *SecretSourceService) UpsertBinding(ctx context.Context, projectID string, req secretsourcetypes.UpsertBindingRequest) (*secretsourcetypes.Binding, error) {
	if strings.TrimSpace(req.RemoteProjectID) == "" || strings.TrimSpace(req.Environment) == "" {
		return nil, common.Classify(common.ErrSecretBindingInvalid, errors.New("project and environment are required"))
	}
	if _, err := s.loadSourceInternal(ctx, req.SourceID); err != nil {
		return nil, err
	}

	binding, err := s.loadBindingInternal(ctx, projectID)
	switch {
	case errors.Is(err, common.ErrSecretBindingNotFound):
		salt, saltErr := newHashSaltInternal()
		if saltErr != nil {
			return nil, saltErr
		}
		binding = &ProjectSecretBinding{ProjectID: projectID, HashSalt: salt}
	case err != nil:
		return nil, err
	}

	binding.SourceID = req.SourceID
	binding.Source = nil
	binding.RemoteProjectID = strings.TrimSpace(req.RemoteProjectID)
	binding.Environment = strings.TrimSpace(req.Environment)
	binding.SecretPath = infisical.NormalizeSecretPath(req.SecretPath)
	binding.IncludeImports = req.IncludeImports
	binding.ExpandReferences = req.ExpandReferences
	binding.Required = req.Required
	binding.Enabled = req.Enabled
	binding.LastFetchError = nil

	if saveErr := s.db.WithContext(ctx).Save(binding).Error; saveErr != nil {
		return nil, fmt.Errorf("failed to save project secret binding: %w", saveErr)
	}
	return s.GetBinding(ctx, projectID)
}

func (s *SecretSourceService) DeleteBinding(ctx context.Context, projectID string) error {
	result := s.db.WithContext(ctx).Delete(&ProjectSecretBinding{}, "project_id = ?", projectID)
	if result.Error != nil {
		return fmt.Errorf("failed to delete project secret binding: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return common.ErrSecretBindingNotFound
	}
	return nil
}

// CheckBinding fetches the bound secrets now and reports key names, .env keys
// they override, and whether a redeploy is needed. No values are returned.
func (s *SecretSourceService) CheckBinding(ctx context.Context, project ProjectRef, actor usertypes.Actor) (secretsourcetypes.CheckResult, error) {
	binding, err := s.loadBindingInternal(ctx, project.ID)
	if err != nil {
		return secretsourcetypes.CheckResult{}, err
	}
	values, invalid, err := s.fetchInternal(ctx, binding, project, actor, "check", false)
	if err != nil {
		return secretsourcetypes.CheckResult{}, err
	}

	keys := sortedKeysInternal(values)
	result := secretsourcetypes.CheckResult{
		Keys:           keys,
		OverriddenKeys: overriddenKeysInternal(ctx, project, keys),
		InvalidKeys:    invalid,
		FetchedAt:      time.Now(),
		DeployedAt:     binding.DeployedAt,
		NeverDeployed:  binding.DeployedHash == nil,
	}
	if binding.DeployedHash != nil {
		result.RedeployNeeded = hashValuesInternal(binding.HashSalt, values) != *binding.DeployedHash
	}
	return result, nil
}

// ResolveDeployEnv fetches the project's bound secrets for a deploy. It
// returns a nil Values map when the project has no enabled binding. When the
// fetch fails, a required binding returns an error and blocks the deploy; an
// optional one logs the failure and lets the deploy continue without secrets.
func (s *SecretSourceService) ResolveDeployEnv(ctx context.Context, project ProjectRef, actor usertypes.Actor) (DeployEnv, error) {
	binding, err := s.loadBindingInternal(ctx, project.ID)
	if errors.Is(err, common.ErrSecretBindingNotFound) {
		return DeployEnv{}, nil
	}
	if err != nil {
		return DeployEnv{}, err
	}
	if !binding.Enabled {
		return DeployEnv{}, nil
	}

	values, _, err := s.fetchInternal(ctx, binding, project, actor, "deploy", binding.Required)
	if err != nil {
		if binding.Required {
			return DeployEnv{}, err
		}
		slog.WarnContext(ctx, "optional secret binding failed; deploying without its secrets", "projectId", project.ID, "error", err)
		return DeployEnv{}, nil
	}
	return DeployEnv{Values: values, Hash: hashValuesInternal(binding.HashSalt, values)}, nil
}

// RecordDeployed stores the hash of the secret set a successful deploy used.
func (s *SecretSourceService) RecordDeployed(ctx context.Context, projectID, hash string) {
	if hash == "" {
		return
	}
	err := s.db.WithContext(ctx).Model(&ProjectSecretBinding{}).Where("project_id = ?", projectID).
		UpdateColumns(map[string]any{"deployed_hash": hash, "deployed_at": time.Now()}).Error
	if err != nil {
		slog.WarnContext(ctx, "failed to record deployed secret hash", "projectId", projectID, "error", err)
	}
}

// fetchInternal reads the binding's secrets, drops keys that are not valid
// environment variable names, records the outcome on the binding, and logs an
// event without any secret values. With requireKeys, an empty result is a
// failure: a required binding that delivers nothing is almost always pointed at
// the wrong environment or path.
func (s *SecretSourceService) fetchInternal(
	ctx context.Context,
	binding *ProjectSecretBinding,
	project ProjectRef,
	actor usertypes.Actor,
	reason string,
	requireKeys bool,
) (values map[string]string, invalid []string, err error) {
	client, source, err := s.clientForSourceInternal(binding.Source)
	if err == nil {
		fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
		values, err = client.ListSecrets(fetchCtx, infisical.SecretsQuery{
			ProjectID:              binding.RemoteProjectID,
			Environment:            binding.Environment,
			SecretPath:             binding.SecretPath,
			IncludeImports:         binding.IncludeImports,
			ExpandSecretReferences: binding.ExpandReferences,
		})
		cancel()
	}

	if err == nil {
		for key := range values {
			if !envKeyPattern.MatchString(key) {
				invalid = append(invalid, key)
				delete(values, key)
			}
		}
		slices.Sort(invalid)
		if requireKeys && len(values) == 0 {
			err = fmt.Errorf("infisical returned no usable secrets for environment %q, path %q; check the binding or turn off Required",
				binding.Environment, binding.SecretPath)
		}
	}

	now := time.Now()
	metadata := database.JSON{
		"reason":      reason,
		"sourceId":    binding.SourceID,
		"environment": binding.Environment,
		"secretPath":  binding.SecretPath,
	}
	if source != nil {
		metadata["sourceName"] = source.Name
	}

	if err != nil {
		message := err.Error()
		s.updateFetchStatusInternal(ctx, binding.ID, now, &message)
		metadata["error"] = message
		s.logEventInternal(ctx, event.EventTypeProjectSecretsError, project, actor, metadata)
		return nil, nil, common.Classify(common.ErrSecretFetchFailed, fmt.Errorf("failed to fetch secrets for project %q: %w", project.Name, err))
	}

	s.updateFetchStatusInternal(ctx, binding.ID, now, nil)
	metadata["keyCount"] = len(values)
	metadata["skippedKeyCount"] = len(invalid)
	s.logEventInternal(ctx, event.EventTypeProjectSecretsFetch, project, actor, metadata)
	return values, invalid, nil
}

func (s *SecretSourceService) updateFetchStatusInternal(ctx context.Context, bindingID string, at time.Time, fetchError *string) {
	err := s.db.WithContext(ctx).Model(&ProjectSecretBinding{}).Where("id = ?", bindingID).
		UpdateColumns(map[string]any{"last_fetched_at": at, "last_fetch_error": fetchError}).Error
	if err != nil {
		slog.WarnContext(ctx, "failed to record secret fetch status", "bindingId", bindingID, "error", err)
	}
}

func (s *SecretSourceService) logEventInternal(ctx context.Context, eventType event.EventType, project ProjectRef, actor usertypes.Actor, metadata database.JSON) {
	if s.eventService == nil {
		return
	}
	if err := s.eventService.LogProjectEvent(ctx, eventType, project.ID, project.Name, actor.ID, actor.Username, localEnvironmentID, metadata); err != nil {
		slog.WarnContext(ctx, "failed to log project secrets event", "projectId", project.ID, "error", err)
	}
}

// ---- Clients ----

func (s *SecretSourceService) clientForSourceIDInternal(ctx context.Context, sourceID string) (*infisical.Client, error) {
	source, err := s.loadSourceInternal(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	client, _, err := s.clientForSourceInternal(source)
	return client, err
}

// clientForSourceInternal reuses one client per source so its access token is
// cached across deploys. Editing the source invalidates the cached client.
func (s *SecretSourceService) clientForSourceInternal(source *SecretSource) (*infisical.Client, *SecretSource, error) {
	if source == nil {
		return nil, nil, common.ErrSecretSourceNotFound
	}
	fingerprint := source.SiteURL + "\x00" + source.ClientID + "\x00" + source.ClientSecret + "\x00" + source.OrganizationSlug

	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	if cached, ok := s.clients[source.ID]; ok && cached.fingerprint == fingerprint {
		return cached.client, source, nil
	}

	secret, err := crypto.Decrypt(source.ClientSecret)
	if err != nil {
		return nil, source, fmt.Errorf("failed to decrypt client secret for source %q: %w", source.Name, err)
	}
	client, err := infisical.NewClient(s.httpClient, infisical.Config{
		SiteURL:          source.SiteURL,
		ClientID:         source.ClientID,
		ClientSecret:     secret,
		OrganizationSlug: source.OrganizationSlug,
	})
	if err != nil {
		return nil, source, err
	}
	s.clients[source.ID] = cachedClient{fingerprint: fingerprint, client: client}
	return client, source, nil
}

func (s *SecretSourceService) forgetClientInternal(sourceID string) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, sourceID)
}

// ---- Persistence helpers ----

func (s *SecretSourceService) loadSourceInternal(ctx context.Context, id string) (*SecretSource, error) {
	if strings.TrimSpace(id) == "" {
		return nil, common.ErrSecretSourceNotFound
	}
	var source SecretSource
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&source).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, common.ErrSecretSourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load secret source: %w", err)
	}
	return &source, nil
}

func (s *SecretSourceService) loadBindingInternal(ctx context.Context, projectID string) (*ProjectSecretBinding, error) {
	var binding ProjectSecretBinding
	err := s.db.WithContext(ctx).Preload("Source").Where("project_id = ?", projectID).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, common.ErrSecretBindingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load project secret binding: %w", err)
	}
	return &binding, nil
}

func (s *SecretSourceService) bindingCountsInternal(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		SourceID string
		Count    int
	}
	err := s.db.WithContext(ctx).Model(&ProjectSecretBinding{}).
		Select("source_id, COUNT(*) AS count").Group("source_id").Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("failed to count secret bindings: %w", err)
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.SourceID] = row.Count
	}
	return counts, nil
}

func (s *SecretSourceService) ensureNameAvailableInternal(ctx context.Context, name, exceptID string) error {
	query := s.db.WithContext(ctx).Model(&SecretSource{}).Where("LOWER(name) = LOWER(?)", name)
	if exceptID != "" {
		query = query.Where("id <> ?", exceptID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("failed to check secret source name: %w", err)
	}
	if count > 0 {
		return common.ErrSecretSourceConflict
	}
	return nil
}

// ---- Pure helpers ----

func normalizeSiteURLInternal(raw string) (string, error) {
	parsed, err := infisical.ParseSiteURL(raw)
	if err != nil {
		return "", common.Classify(common.ErrSecretSourceInvalid, err)
	}
	return parsed.String(), nil
}

func sameConnectionInternal(source *SecretSource, cfg infisical.Config) bool {
	siteURL, err := normalizeSiteURLInternal(cfg.SiteURL)
	if err != nil {
		return false
	}
	return siteURL == source.SiteURL &&
		strings.TrimSpace(cfg.ClientID) == source.ClientID &&
		strings.TrimSpace(cfg.OrganizationSlug) == source.OrganizationSlug
}

func newHashSaltInternal() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate hash salt: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// hashValuesInternal returns a salted HMAC over the sorted key/value set, so
// two deploys can be compared without storing anything derived from a single
// secret in a guessable way.
func hashValuesInternal(salt string, values map[string]string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	for _, key := range sortedKeysInternal(values) {
		_, _ = mac.Write([]byte(key))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(values[key]))
		_, _ = mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func sortedKeysInternal(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// overriddenKeysInternal lists secret keys that are also set in the project's
// .env or the environment's .env.global. Read failures only hide the warning.
func overriddenKeysInternal(ctx context.Context, project ProjectRef, keys []string) []string {
	defined := make(map[string]struct{})
	paths := make([]string, 0, 2)
	if project.ProjectsDirectory != "" {
		paths = append(paths, filepath.Join(project.ProjectsDirectory, projects.GlobalEnvFileName))
	}
	if project.Path != "" {
		paths = append(paths, filepath.Join(project.Path, projects.EffectiveEnvFileName))
	}
	for _, path := range paths {
		values, err := projects.ParseProjectEnvFile(path, nil)
		if err != nil {
			slog.DebugContext(ctx, "could not read env file for override check", "path", path, "error", err)
			continue
		}
		for key := range values {
			defined[key] = struct{}{}
		}
	}

	overridden := make([]string, 0)
	for _, key := range keys {
		if _, ok := defined[key]; ok {
			overridden = append(overridden, key)
		}
	}
	return overridden
}

func sourceToDTOInternal(source *SecretSource, bindingCount int) secretsourcetypes.Source {
	return secretsourcetypes.Source{
		ID:               source.ID,
		Name:             source.Name,
		Provider:         source.Provider,
		SiteURL:          source.SiteURL,
		ClientID:         source.ClientID,
		OrganizationSlug: source.OrganizationSlug,
		HasClientSecret:  source.ClientSecret != "",
		BindingCount:     bindingCount,
		LastTestedAt:     source.LastTestedAt,
		LastTestError:    source.LastTestError,
		CreatedAt:        source.CreatedAt,
		UpdatedAt:        source.UpdatedAt,
	}
}

func bindingToDTOInternal(binding *ProjectSecretBinding) secretsourcetypes.Binding {
	dto := secretsourcetypes.Binding{
		ProjectID:        binding.ProjectID,
		SourceID:         binding.SourceID,
		RemoteProjectID:  binding.RemoteProjectID,
		Environment:      binding.Environment,
		SecretPath:       binding.SecretPath,
		IncludeImports:   binding.IncludeImports,
		ExpandReferences: binding.ExpandReferences,
		Required:         binding.Required,
		Enabled:          binding.Enabled,
		DeployedAt:       binding.DeployedAt,
		LastFetchedAt:    binding.LastFetchedAt,
		LastFetchError:   binding.LastFetchError,
	}
	if binding.Source != nil {
		dto.SourceName = binding.Source.Name
	}
	return dto
}
