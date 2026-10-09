package secretsource

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"
	usertypes "github.com/getarcaneapp/arcane/types/v2/user"
	"github.com/libtnb/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.getarcane.app/sys/crypto"
	"gorm.io/gorm"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/internal/database"
)

// fakeInfisicalInternal serves login and a mutable secret set.
type fakeInfisicalInternal struct {
	mu      sync.Mutex
	secrets string
	fail    bool
}

func (f *fakeInfisicalInternal) setSecrets(body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets = body
}

func (f *fakeInfisicalInternal) setFail(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *fakeInfisicalInternal) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/universal-auth/login", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"tok","expiresIn":3600,"accessTokenMaxTTL":7200,"tokenType":"Bearer"}`))
	})
	mux.HandleFunc("GET /api/v4/secrets", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.fail {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Permission denied"}`))
			return
		}
		_, _ = w.Write([]byte(f.secrets))
	})
	mux.HandleFunc("GET /api/v1/projects", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"projects":[{"id":"p1","name":"Apps","slug":"apps","environments":[{"id":"e1","name":"Production","slug":"prod"}]}]}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// fakeBitwardenServeInternal mimics the bw serve Vault Management API.
func fakeBitwardenServeInternal(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"object":"template","template":{"serverUrl":"https://vault.example","userEmail":"deploy@example","status":"unlocked"}}}`))
	})
	mux.HandleFunc("POST /sync", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"object":"message","title":"Syncing complete."}}`))
	})
	mux.HandleFunc("GET /list/object/items", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("folderid") != "f1" {
			_, _ = w.Write([]byte(`{"success":true,"data":{"object":"list","data":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"object":"list","data":[
			{"id":"i1","name":"DB_PASSWORD","type":1,"login":{"password":"pw"}},
			{"id":"i2","name":"TLS_NOTE","type":2,"notes":"note text"},
			{"id":"i3","name":"EMPTY_LOGIN","type":1,"login":{"password":null}}
		]}}`))
	})
	mux.HandleFunc("GET /object/item/i9", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"object":"item","id":"i9","name":"billing env","type":2,
			"fields":[{"name":"API_KEY","value":"k","type":1},{"name":"DEBUG","value":"true","type":2},{"name":"user","value":null,"type":3}]}}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func setupSecretSourceServiceTestInternal(t *testing.T) (*SecretSourceService, *database.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&SecretSource{}, &ProjectSecretBinding{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	crypto.InitEncryption(&crypto.Config{
		EncryptionKey: "test-encryption-key-for-testing-32bytes-min",
		Environment:   "test",
	})

	wrapped := &database.DB{DB: db}
	return NewSecretSourceService(wrapped, nil), wrapped
}

func infisicalSettingsInternal(siteURL, clientID string) secretsourcetypes.SourceSettings {
	return secretsourcetypes.SourceSettings{Infisical: &secretsourcetypes.InfisicalSettings{SiteURL: siteURL, ClientID: clientID}}
}

func createInfisicalSourceInternal(t *testing.T, service *SecretSourceService, siteURL string) *secretsourcetypes.Source {
	t.Helper()
	source, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name:       "Homelab Infisical",
		Provider:   secretsourcetypes.ProviderInfisical,
		Settings:   infisicalSettingsInternal(siteURL, "client-id"),
		Credential: "client-secret",
	})
	require.NoError(t, err)
	return source
}

func bindInfisicalInternal(t *testing.T, service *SecretSourceService, sourceID string, required, autoRedeploy bool) {
	t.Helper()
	_, err := upsertBindingForTestInternal(t.Context(), service, "project-1", secretsourcetypes.UpsertBindingRequest{
		SourceID: sourceID,
		Target: secretsourcetypes.BindingTarget{Infisical: &secretsourcetypes.InfisicalTarget{
			ProjectID: "p1", Environment: "prod", SecretPath: "billing", IncludeImports: true, ExpandReferences: true,
		}},
		Required:     required,
		Enabled:      true,
		AutoRedeploy: autoRedeploy,
	})
	require.NoError(t, err)
}

// The helpers below keep the single-binding tests readable: each project in
// them has at most one binding.

func upsertBindingForTestInternal(
	ctx context.Context,
	service *SecretSourceService,
	projectID string,
	req secretsourcetypes.UpsertBindingRequest,
) (*secretsourcetypes.Binding, error) {
	existing, err := service.ListBindings(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return service.UpdateBinding(ctx, projectID, existing[0].ID, req)
	}
	return service.CreateBinding(ctx, projectID, req)
}

func firstBindingForTestInternal(ctx context.Context, service *SecretSourceService, projectID string) (*secretsourcetypes.Binding, error) {
	bindings, err := service.ListBindings(ctx, projectID)
	if err != nil || len(bindings) == 0 {
		return nil, err
	}
	return &bindings[0], nil
}

func checkFirstBindingForTestInternal(
	ctx context.Context,
	service *SecretSourceService,
	project ProjectRef,
	actor usertypes.Actor,
) (secretsourcetypes.CheckResult, error) {
	result, err := service.CheckBindings(ctx, project, actor)
	if err != nil {
		return secretsourcetypes.CheckResult{}, err
	}
	first := result.Bindings[0]
	if first.Error != "" {
		return first, errors.New(first.Error)
	}
	return first, nil
}

func deleteBindingsForTestInternal(ctx context.Context, service *SecretSourceService, projectID string) error {
	bindings, err := service.ListBindings(ctx, projectID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if deleteErr := service.DeleteBinding(ctx, projectID, binding.ID); deleteErr != nil {
			return deleteErr
		}
	}
	return nil
}

func TestCreateSourceValidatesPerProvider(t *testing.T) {
	service, db := setupSecretSourceServiceTestInternal(t)

	source := createInfisicalSourceInternal(t, service, "https://infisical.example.ts.net/")
	assert.True(t, source.HasCredential)
	require.NotNil(t, source.Settings.Infisical)
	assert.Equal(t, "https://infisical.example.ts.net", source.Settings.Infisical.SiteURL)

	var stored SecretSource
	require.NoError(t, db.First(&stored, "id = ?", source.ID).Error)
	assert.NotEqual(t, "client-secret", stored.Credential)
	plain, err := crypto.Decrypt(stored.Credential)
	require.NoError(t, err)
	assert.Equal(t, "client-secret", plain)

	_, err = service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "homelab infisical", Provider: secretsourcetypes.ProviderInfisical,
		Settings: infisicalSettingsInternal("", "other"), Credential: "other",
	})
	require.ErrorIs(t, err, common.ErrSecretSourceConflict)

	_, err = service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "No secret", Provider: secretsourcetypes.ProviderInfisical, Settings: infisicalSettingsInternal("", "id"),
	})
	require.ErrorIs(t, err, common.ErrValidation)

	vault, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "Vaultwarden", Provider: secretsourcetypes.ProviderBitwarden,
		Settings: secretsourcetypes.SourceSettings{
			Bitwarden: &secretsourcetypes.BitwardenSettings{ServeURL: "http://bw-serve:8087/"},
			Infisical: &secretsourcetypes.InfisicalSettings{ClientID: "ignored"},
		},
	})
	require.NoError(t, err)
	assert.False(t, vault.HasCredential)
	assert.Nil(t, vault.Settings.Infisical, "settings of another provider must be dropped")
	assert.Equal(t, "http://bw-serve:8087", vault.Settings.Bitwarden.ServeURL)

	_, err = service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{Name: "Unknown", Provider: "nope"})
	require.ErrorIs(t, err, common.ErrValidation)
}

func TestCreateSourceCredentialsForNewProviders(t *testing.T) {
	service, db := setupSecretSourceServiceTestInternal(t)

	// Vault/OpenBao, Doppler, and 1Password need a credential.
	needs := map[string]secretsourcetypes.SourceSettings{
		secretsourcetypes.ProviderVault:       {Vault: &secretsourcetypes.VaultSettings{Address: "http://openbao:8200"}},
		secretsourcetypes.ProviderDoppler:     {Doppler: &secretsourcetypes.DopplerSettings{}},
		secretsourcetypes.ProviderOnePassword: {OnePassword: &secretsourcetypes.OnePasswordSettings{ServerURL: "http://op-connect-api:8080"}},
	}
	for provider, settings := range needs {
		_, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{Name: "no " + provider, Provider: provider, Settings: settings})
		require.ErrorIs(t, err, common.ErrValidation, provider)

		created, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{Name: provider, Provider: provider, Settings: settings, Credential: "tok"})
		require.NoError(t, err, provider)
		assert.True(t, created.HasCredential, provider)
	}

	// The HTTP token is optional.
	open, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "kit", Provider: secretsourcetypes.ProviderHTTP,
		Settings: secretsourcetypes.SourceSettings{HTTP: &secretsourcetypes.HTTPSettings{BaseURL: "http://sops-kit:8080/"}},
	})
	require.NoError(t, err)
	assert.False(t, open.HasCredential)
	assert.Equal(t, "http://sops-kit:8080", open.Settings.HTTP.BaseURL)

	withToken, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "kit with token", Provider: secretsourcetypes.ProviderHTTP, Credential: "bearer",
		Settings: secretsourcetypes.SourceSettings{HTTP: &secretsourcetypes.HTTPSettings{BaseURL: "http://sops-kit:8080"}},
	})
	require.NoError(t, err)
	assert.True(t, withToken.HasCredential)

	// A Vault setup token is stored apart from the deploy token and needs
	// its secret when first enabled.
	_, err = service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "bao no setup secret", Provider: secretsourcetypes.ProviderVault, Credential: "read",
		Settings: secretsourcetypes.SourceSettings{Vault: &secretsourcetypes.VaultSettings{Address: "http://openbao:8200", SetupToken: true}},
	})
	require.ErrorIs(t, err, common.ErrValidation)

	bao, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "bao", Provider: secretsourcetypes.ProviderVault, Credential: "read", SetupCredential: "write",
		Settings: secretsourcetypes.SourceSettings{Vault: &secretsourcetypes.VaultSettings{Address: "http://openbao:8200", SetupToken: true}},
	})
	require.NoError(t, err)
	assert.True(t, bao.HasSetupCredential)
	var stored SecretSource
	require.NoError(t, db.First(&stored, "id = ?", bao.ID).Error)
	setupPlain, err := crypto.Decrypt(stored.SetupCredential)
	require.NoError(t, err)
	assert.Equal(t, "write", setupPlain)

	// Turning the setup token off deletes it.
	off := secretsourcetypes.SourceSettings{Vault: &secretsourcetypes.VaultSettings{Address: "http://openbao:8200"}}
	updated, err := service.UpdateSource(t.Context(), bao.ID, secretsourcetypes.UpdateSourceRequest{Settings: &off})
	require.NoError(t, err)
	assert.False(t, updated.HasSetupCredential)
	assert.True(t, updated.HasCredential)
}

func TestUpsertBindingNormalizesNewProviderTargets(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	bao, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "bao", Provider: secretsourcetypes.ProviderVault, Credential: "read",
		Settings: secretsourcetypes.SourceSettings{Vault: &secretsourcetypes.VaultSettings{Address: "http://openbao:8200"}},
	})
	require.NoError(t, err)

	binding, err := upsertBindingForTestInternal(t.Context(), service, "project-1", secretsourcetypes.UpsertBindingRequest{
		SourceID: bao.ID, Enabled: true,
		Target: secretsourcetypes.BindingTarget{
			Vault:   &secretsourcetypes.VaultTarget{Path: "/apps/web/"},
			Doppler: &secretsourcetypes.DopplerTarget{Project: "ignored", Config: "ignored"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, &secretsourcetypes.VaultTarget{Mount: "secret", Path: "apps/web", KVVersion: 2}, binding.Target.Vault)
	assert.Nil(t, binding.Target.Doppler, "targets of another provider must be dropped")

	_, err = upsertBindingForTestInternal(t.Context(), service, "project-1", secretsourcetypes.UpsertBindingRequest{
		SourceID: bao.ID, Enabled: true,
		Target: secretsourcetypes.BindingTarget{Vault: &secretsourcetypes.VaultTarget{Path: "apps/../sys"}},
	})
	require.ErrorIs(t, err, common.ErrValidation)
}

func TestUpdateSourceKeepsCredentialAndTestResultUnlessConnectionChanges(t *testing.T) {
	service, db := setupSecretSourceServiceTestInternal(t)
	server := (&fakeInfisicalInternal{}).server(t)
	source := createInfisicalSourceInternal(t, service, server.URL)

	_, err := service.TestSource(t.Context(), secretsourcetypes.TestSourceRequest{
		SourceID: source.ID, Settings: infisicalSettingsInternal(server.URL, "client-id"), Credential: "client-secret",
	})
	require.NoError(t, err)

	blank := ""
	newName := "Renamed"
	updated, err := service.UpdateSource(t.Context(), source.ID, secretsourcetypes.UpdateSourceRequest{Name: &newName, Credential: &blank})
	require.NoError(t, err)
	assert.Equal(t, "Renamed", updated.Name)
	require.NotNil(t, updated.LastTestedAt, "a rename must keep the test result")

	var stored SecretSource
	require.NoError(t, db.First(&stored, "id = ?", source.ID).Error)
	plain, err := crypto.Decrypt(stored.Credential)
	require.NoError(t, err)
	assert.Equal(t, "client-secret", plain)

	changed := infisicalSettingsInternal(server.URL, "other-client")
	updated, err = service.UpdateSource(t.Context(), source.ID, secretsourcetypes.UpdateSourceRequest{Settings: &changed})
	require.NoError(t, err)
	assert.Nil(t, updated.LastTestedAt, "changing the connection must clear the test result")

	// Testing unsaved settings must not overwrite the stored status.
	_, err = service.TestSource(t.Context(), secretsourcetypes.TestSourceRequest{
		SourceID: source.ID, Settings: infisicalSettingsInternal(server.URL, "unsaved-client"),
	})
	require.NoError(t, err)
	current, err := service.GetSource(t.Context(), source.ID)
	require.NoError(t, err)
	assert.Nil(t, current.LastTestedAt)
}

func TestDeleteSourceInUseIsRejected(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	source := createInfisicalSourceInternal(t, service, "")
	bindInfisicalInternal(t, service, source.ID, true, false)

	require.ErrorIs(t, service.DeleteSource(t.Context(), source.ID), common.ErrSecretSourceInUse)
	require.NoError(t, deleteBindingsForTestInternal(t.Context(), service, "project-1"))
	require.NoError(t, service.DeleteSource(t.Context(), source.ID))
}

func TestUpsertBindingValidatesTargetForProvider(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	source := createInfisicalSourceInternal(t, service, "")

	_, err := upsertBindingForTestInternal(t.Context(), service, "project-1", secretsourcetypes.UpsertBindingRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.BindingTarget{Bitwarden: &secretsourcetypes.BitwardenTarget{Scope: "folder", ID: "f1"}},
	})
	require.ErrorIs(t, err, common.ErrValidation, "an Infisical source needs an Infisical target")
}

func TestResolveDeployEnvDropsInvalidKeysAndHashesStably(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	fake := &fakeInfisicalInternal{}
	fake.setSecrets(`{"secrets":[{"secretKey":"DB_PASS","secretValue":"s3cret"},{"secretKey":"bad-key","secretValue":"x"}]}`)
	source := createInfisicalSourceInternal(t, service, fake.server(t).URL)
	bindInfisicalInternal(t, service, source.ID, true, false)

	project := ProjectRef{ID: "project-1", Name: "billing-api"}
	first, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"DB_PASS": "s3cret"}, first.Values)
	require.NotEmpty(t, first.Hash)

	second, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Equal(t, first.Hash, second.Hash)
}

func TestResolveDeployEnvRequiredVersusOptional(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	fake := &fakeInfisicalInternal{}
	fake.setFail(true)
	source := createInfisicalSourceInternal(t, service, fake.server(t).URL)
	project := ProjectRef{ID: "project-1", Name: "billing-api"}

	bindInfisicalInternal(t, service, source.ID, true, false)
	_, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.ErrorIs(t, err, common.ErrSecretFetchFailed)
	assert.Contains(t, err.Error(), "Permission denied")

	binding, err := firstBindingForTestInternal(t.Context(), service, "project-1")
	require.NoError(t, err)
	require.NotNil(t, binding.LastFetchError)

	bindInfisicalInternal(t, service, source.ID, false, false)
	env, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Nil(t, env.Values)
}

func TestResolveDeployEnvEmptyResultFailsOnlyWhenRequired(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	fake := &fakeInfisicalInternal{}
	fake.setSecrets(`{"secrets":[{"secretKey":"bad-key","secretValue":"x"}]}`)
	source := createInfisicalSourceInternal(t, service, fake.server(t).URL)
	project := ProjectRef{ID: "project-1", Name: "billing-api"}

	bindInfisicalInternal(t, service, source.ID, true, false)
	_, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.ErrorIs(t, err, common.ErrSecretFetchFailed)
	assert.Contains(t, err.Error(), "no usable secrets")

	result, err := checkFirstBindingForTestInternal(t.Context(), service, project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Empty(t, result.Keys)
	assert.Equal(t, []string{"bad-key"}, result.InvalidKeys)

	bindInfisicalInternal(t, service, source.ID, false, false)
	env, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Empty(t, env.Values)
}

func TestResolveDeployEnvSkipsDisabledAndUnboundProjects(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	source := createInfisicalSourceInternal(t, service, "http://127.0.0.1:1")

	env, err := service.ResolveDeployEnv(t.Context(), ProjectRef{ID: "unbound"}, usertypes.Actor{})
	require.NoError(t, err)
	assert.Nil(t, env.Values)

	_, err = upsertBindingForTestInternal(t.Context(), service, "project-1", secretsourcetypes.UpsertBindingRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.BindingTarget{Infisical: &secretsourcetypes.InfisicalTarget{ProjectID: "p1", Environment: "prod"}},
		Required: true,
		Enabled:  false,
	})
	require.NoError(t, err)
	env, err = service.ResolveDeployEnv(t.Context(), ProjectRef{ID: "project-1"}, usertypes.Actor{})
	require.NoError(t, err)
	assert.Nil(t, env.Values)
}

func TestCheckBindingReportsOverridesAndDrift(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	fake := &fakeInfisicalInternal{}
	fake.setSecrets(`{"secrets":[{"secretKey":"DB_PASS","secretValue":"one"},{"secretKey":"API_KEY","secretValue":"k"}]}`)
	source := createInfisicalSourceInternal(t, service, fake.server(t).URL)
	bindInfisicalInternal(t, service, source.ID, true, false)

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".env"), []byte("DB_PASS=local\nOTHER=1\n"), 0o600))
	project := ProjectRef{ID: "project-1", Name: "billing-api", Path: projectDir}

	result, err := checkFirstBindingForTestInternal(t.Context(), service, project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Equal(t, []string{"API_KEY", "DB_PASS"}, result.Keys)
	assert.Equal(t, []string{"DB_PASS"}, result.OverriddenKeys)
	assert.True(t, result.NeverDeployed)

	deployed, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	service.RecordDeployed(t.Context(), project.ID, deployed)

	result, err = checkFirstBindingForTestInternal(t.Context(), service, project, usertypes.Actor{})
	require.NoError(t, err)
	assert.False(t, result.RedeployNeeded)

	fake.setSecrets(`{"secrets":[{"secretKey":"DB_PASS","secretValue":"two"},{"secretKey":"API_KEY","secretValue":"k"}]}`)
	result, err = checkFirstBindingForTestInternal(t.Context(), service, project, usertypes.Actor{})
	require.NoError(t, err)
	assert.True(t, result.RedeployNeeded)

	binding, err := firstBindingForTestInternal(t.Context(), service, project.ID)
	require.NoError(t, err)
	assert.True(t, binding.RedeployNeeded, "the binding remembers what the last check saw")
}

func TestCheckDriftReportsEachChangeOnce(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	fake := &fakeInfisicalInternal{}
	fake.setSecrets(`{"secrets":[{"secretKey":"DB_PASS","secretValue":"one"}]}`)
	source := createInfisicalSourceInternal(t, service, fake.server(t).URL)
	bindInfisicalInternal(t, service, source.ID, true, true)
	project := ProjectRef{ID: "project-1", Name: "billing-api"}
	resolve := func(_ context.Context, _ string) (ProjectRef, error) { return project, nil }

	// Never deployed: nothing to compare against.
	assert.Empty(t, service.CheckDrift(t.Context(), resolve))

	deployed, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	service.RecordDeployed(t.Context(), project.ID, deployed)
	assert.Empty(t, service.CheckDrift(t.Context(), resolve), "unchanged secrets are not drift")

	fake.setSecrets(`{"secrets":[{"secretKey":"DB_PASS","secretValue":"two"}]}`)
	changes := service.CheckDrift(t.Context(), resolve)
	require.Len(t, changes, 1)
	assert.Equal(t, "project-1", changes[0].ProjectID)
	assert.True(t, changes[0].AutoRedeploy)

	assert.Empty(t, service.CheckDrift(t.Context(), resolve), "the same change is reported only once")

	fake.setSecrets(`{"secrets":[{"secretKey":"DB_PASS","secretValue":"three"}]}`)
	assert.Len(t, service.CheckDrift(t.Context(), resolve), 1, "a further change is reported again")

	redeployed, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	service.RecordDeployed(t.Context(), project.ID, redeployed)
	binding, err := firstBindingForTestInternal(t.Context(), service, project.ID)
	require.NoError(t, err)
	assert.False(t, binding.RedeployNeeded)
}

func TestBitwardenSourceFolderAndItemBindings(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	server := fakeBitwardenServeInternal(t)
	source, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "Vaultwarden", Provider: secretsourcetypes.ProviderBitwarden,
		Settings: secretsourcetypes.SourceSettings{Bitwarden: &secretsourcetypes.BitwardenSettings{ServeURL: server.URL}},
	})
	require.NoError(t, err)

	result, err := service.TestSource(t.Context(), secretsourcetypes.TestSourceRequest{SourceID: source.ID, Settings: source.Settings})
	require.NoError(t, err)
	assert.True(t, result.OK, result.Message)
	assert.Contains(t, result.Message, "deploy@example")

	project := ProjectRef{ID: "project-1", Name: "billing-api"}
	_, err = upsertBindingForTestInternal(t.Context(), service, project.ID, secretsourcetypes.UpsertBindingRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.BindingTarget{Bitwarden: &secretsourcetypes.BitwardenTarget{Scope: "folder", ID: "f1", Name: "billing"}},
		Required: true, Enabled: true,
	})
	require.NoError(t, err)
	check, err := checkFirstBindingForTestInternal(t.Context(), service, project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Equal(t, []string{"DB_PASSWORD", "TLS_NOTE"}, check.Keys)
	assert.Equal(t, []string{"EMPTY_LOGIN"}, check.InvalidKeys)

	_, err = upsertBindingForTestInternal(t.Context(), service, project.ID, secretsourcetypes.UpsertBindingRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.BindingTarget{Bitwarden: &secretsourcetypes.BitwardenTarget{Scope: "item", ID: "i9"}},
		Required: true, Enabled: true,
	})
	require.NoError(t, err)
	env, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"API_KEY": "k", "DEBUG": "true"}, env.Values)
}

func TestStoredCredentialOnlyGoesToItsOwnAddress(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)

	var seen []string
	capture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"A":"1"}`))
	}))
	t.Cleanup(capture.Close)

	source, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "kit", Provider: secretsourcetypes.ProviderHTTP, Credential: "stored-token",
		Settings: secretsourcetypes.SourceSettings{HTTP: &secretsourcetypes.HTTPSettings{BaseURL: "http://sops-kit:8080"}},
	})
	require.NoError(t, err)

	// Testing an edited address with a blank credential must not send the stored token there.
	moved := secretsourcetypes.SourceSettings{HTTP: &secretsourcetypes.HTTPSettings{BaseURL: capture.URL}}
	result, err := service.TestSource(t.Context(), secretsourcetypes.TestSourceRequest{SourceID: source.ID, Settings: moved})
	require.NoError(t, err)
	assert.True(t, result.OK)
	require.Len(t, seen, 1)
	assert.Empty(t, seen[0])

	// Saving the new address needs the token again, or an explicit removal.
	_, err = service.UpdateSource(t.Context(), source.ID, secretsourcetypes.UpdateSourceRequest{Settings: &moved})
	require.ErrorIs(t, err, common.ErrValidation)
	updated, err := service.UpdateSource(t.Context(), source.ID, secretsourcetypes.UpdateSourceRequest{Settings: &moved, ClearCredential: true})
	require.NoError(t, err)
	assert.False(t, updated.HasCredential)

	// Vault: switching from token to AppRole must not reuse the token as a secret ID.
	bao, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "bao", Provider: secretsourcetypes.ProviderVault, Credential: "token",
		Settings: secretsourcetypes.SourceSettings{Vault: &secretsourcetypes.VaultSettings{Address: "http://openbao:8200"}},
	})
	require.NoError(t, err)
	approle := secretsourcetypes.SourceSettings{Vault: &secretsourcetypes.VaultSettings{Address: "http://openbao:8200", AuthMethod: "approle", RoleID: "r"}}
	_, err = service.UpdateSource(t.Context(), bao.ID, secretsourcetypes.UpdateSourceRequest{Settings: &approle})
	require.ErrorIs(t, err, common.ErrValidation)
	_, err = service.UpdateSource(t.Context(), bao.ID, secretsourcetypes.UpdateSourceRequest{Settings: &approle, Credential: new("secret-id")})
	require.NoError(t, err)

	// Only HTTP tokens can be removed.
	_, err = service.UpdateSource(t.Context(), bao.ID, secretsourcetypes.UpdateSourceRequest{ClearCredential: true})
	require.ErrorIs(t, err, common.ErrValidation)
}
