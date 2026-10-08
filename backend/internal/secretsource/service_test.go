package secretsource

import (
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

func createTestSourceInternal(t *testing.T, service *SecretSourceService, siteURL string) *secretsourcetypes.Source {
	t.Helper()
	source, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "Homelab Infisical", SiteURL: siteURL, ClientID: "client-id", ClientSecret: "client-secret",
	})
	require.NoError(t, err)
	return source
}

func bindTestProjectInternal(t *testing.T, service *SecretSourceService, sourceID string, required bool) {
	t.Helper()
	_, err := service.UpsertBinding(t.Context(), "project-1", secretsourcetypes.UpsertBindingRequest{
		SourceID: sourceID, RemoteProjectID: "p1", Environment: "prod", SecretPath: "billing",
		IncludeImports: true, ExpandReferences: true, Required: required, Enabled: true,
	})
	require.NoError(t, err)
}

func TestCreateSourceEncryptsSecretAndRejectsDuplicateNames(t *testing.T) {
	service, db := setupSecretSourceServiceTestInternal(t)

	source := createTestSourceInternal(t, service, "https://infisical.example.ts.net/")
	assert.True(t, source.HasClientSecret)
	assert.Equal(t, "https://infisical.example.ts.net", source.SiteURL)

	var stored SecretSource
	require.NoError(t, db.First(&stored, "id = ?", source.ID).Error)
	assert.NotEqual(t, "client-secret", stored.ClientSecret)
	plain, err := crypto.Decrypt(stored.ClientSecret)
	require.NoError(t, err)
	assert.Equal(t, "client-secret", plain)

	_, err = service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "homelab infisical", ClientID: "other", ClientSecret: "other",
	})
	require.ErrorIs(t, err, common.ErrSecretSourceConflict)

	_, err = service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name: "No secret", ClientID: "id",
	})
	require.ErrorIs(t, err, common.ErrValidation)
}

func TestUpdateSourceKeepsSecretWhenBlank(t *testing.T) {
	service, db := setupSecretSourceServiceTestInternal(t)
	source := createTestSourceInternal(t, service, "")

	blank := ""
	newName := "Renamed"
	updated, err := service.UpdateSource(t.Context(), source.ID, secretsourcetypes.UpdateSourceRequest{Name: &newName, ClientSecret: &blank})
	require.NoError(t, err)
	assert.Equal(t, "Renamed", updated.Name)

	var stored SecretSource
	require.NoError(t, db.First(&stored, "id = ?", source.ID).Error)
	plain, err := crypto.Decrypt(stored.ClientSecret)
	require.NoError(t, err)
	assert.Equal(t, "client-secret", plain)
}

func TestDeleteSourceInUseIsRejected(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	source := createTestSourceInternal(t, service, "")
	bindTestProjectInternal(t, service, source.ID, true)

	require.ErrorIs(t, service.DeleteSource(t.Context(), source.ID), common.ErrSecretSourceInUse)
	require.NoError(t, service.DeleteBinding(t.Context(), "project-1"))
	require.NoError(t, service.DeleteSource(t.Context(), source.ID))
}

func TestResolveDeployEnvDropsInvalidKeysAndHashesStably(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	fake := &fakeInfisicalInternal{}
	fake.setSecrets(`{"secrets":[{"secretKey":"DB_PASS","secretValue":"s3cret"},{"secretKey":"bad-key","secretValue":"x"}]}`)
	source := createTestSourceInternal(t, service, fake.server(t).URL)
	bindTestProjectInternal(t, service, source.ID, true)

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
	source := createTestSourceInternal(t, service, fake.server(t).URL)
	project := ProjectRef{ID: "project-1", Name: "billing-api"}

	bindTestProjectInternal(t, service, source.ID, true)
	_, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.ErrorIs(t, err, common.ErrSecretFetchFailed)
	assert.Contains(t, err.Error(), "Permission denied")

	binding, err := service.GetBinding(t.Context(), "project-1")
	require.NoError(t, err)
	require.NotNil(t, binding.LastFetchError)

	bindTestProjectInternal(t, service, source.ID, false)
	env, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Nil(t, env.Values)
}

func TestResolveDeployEnvSkipsDisabledAndUnboundProjects(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	source := createTestSourceInternal(t, service, "http://127.0.0.1:1")

	env, err := service.ResolveDeployEnv(t.Context(), ProjectRef{ID: "unbound"}, usertypes.Actor{})
	require.NoError(t, err)
	assert.Nil(t, env.Values)

	_, err = service.UpsertBinding(t.Context(), "project-1", secretsourcetypes.UpsertBindingRequest{
		SourceID: source.ID, RemoteProjectID: "p1", Environment: "prod", Required: true, Enabled: false,
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
	source := createTestSourceInternal(t, service, fake.server(t).URL)
	bindTestProjectInternal(t, service, source.ID, true)

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".env"), []byte("DB_PASS=local\nOTHER=1\n"), 0o600))
	project := ProjectRef{ID: "project-1", Name: "billing-api", Path: projectDir}

	result, err := service.CheckBinding(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.Equal(t, []string{"API_KEY", "DB_PASS"}, result.Keys)
	assert.Equal(t, []string{"DB_PASS"}, result.OverriddenKeys)
	assert.True(t, result.NeverDeployed)
	assert.False(t, result.RedeployNeeded)

	deployed, err := service.ResolveDeployEnv(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	service.RecordDeployed(t.Context(), project.ID, deployed.Hash)

	result, err = service.CheckBinding(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.False(t, result.NeverDeployed)
	assert.False(t, result.RedeployNeeded)

	fake.setSecrets(`{"secrets":[{"secretKey":"DB_PASS","secretValue":"two"},{"secretKey":"API_KEY","secretValue":"k"}]}`)
	result, err = service.CheckBinding(t.Context(), project, usertypes.Actor{})
	require.NoError(t, err)
	assert.True(t, result.RedeployNeeded)
}

func TestTestSourceUsesStoredSecretAndRecordsResult(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	server := (&fakeInfisicalInternal{}).server(t)
	source := createTestSourceInternal(t, service, server.URL)

	result, err := service.TestSource(t.Context(), secretsourcetypes.TestSourceRequest{
		SourceID: source.ID, SiteURL: server.URL, ClientID: "client-id",
	})
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.True(t, result.CanListProjects)
	assert.Equal(t, 1, result.ProjectsVisible)

	stored, err := service.GetSource(t.Context(), source.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.LastTestedAt)
	assert.Nil(t, stored.LastTestError)
}

func TestUpdateSourceKeepsTestResultUnlessConnectionChanges(t *testing.T) {
	service, _ := setupSecretSourceServiceTestInternal(t)
	server := (&fakeInfisicalInternal{}).server(t)
	source := createTestSourceInternal(t, service, server.URL)

	// Testing with the stored settings, secret included, records the result.
	_, err := service.TestSource(t.Context(), secretsourcetypes.TestSourceRequest{
		SourceID: source.ID, SiteURL: server.URL, ClientID: "client-id", ClientSecret: "client-secret",
	})
	require.NoError(t, err)

	newName := "Renamed"
	updated, err := service.UpdateSource(t.Context(), source.ID, secretsourcetypes.UpdateSourceRequest{Name: &newName})
	require.NoError(t, err)
	require.NotNil(t, updated.LastTestedAt, "a rename must keep the test result")

	newClientID := "other-client"
	updated, err = service.UpdateSource(t.Context(), source.ID, secretsourcetypes.UpdateSourceRequest{ClientID: &newClientID})
	require.NoError(t, err)
	assert.Nil(t, updated.LastTestedAt, "changing the connection must clear the test result")

	// Testing unsaved settings must not overwrite the stored status.
	_, err = service.TestSource(t.Context(), secretsourcetypes.TestSourceRequest{
		SourceID: source.ID, SiteURL: server.URL, ClientID: "unsaved-client",
	})
	require.NoError(t, err)
	stored, err := service.GetSource(t.Context(), source.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.LastTestedAt)
}
