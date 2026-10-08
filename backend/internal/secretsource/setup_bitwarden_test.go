package secretsource

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"
	usertypes "github.com/getarcaneapp/arcane/types/v2/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bwVaultInternal is a stateful fake bw serve: folders and items in memory.
type bwVaultInternal struct {
	mu      sync.Mutex
	folders map[string]string         // id -> name
	items   map[string]map[string]any // id -> item
	nextID  int
}

func (v *bwVaultInternal) server(t *testing.T) *httptest.Server {
	t.Helper()
	ok := func(w http.ResponseWriter, data any) {
		writeJSONInternal(w, http.StatusOK, map[string]any{"success": true, "data": data})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		ok(w, map[string]any{"template": map[string]string{"status": "unlocked", "userEmail": "deploy@example", "serverUrl": "https://vault"}})
	})
	mux.HandleFunc("POST /sync", func(w http.ResponseWriter, _ *http.Request) { ok(w, map[string]string{"title": "Syncing complete."}) })
	mux.HandleFunc("GET /list/object/folders", func(w http.ResponseWriter, _ *http.Request) {
		v.mu.Lock()
		defer v.mu.Unlock()
		list := []map[string]any{}
		for id, name := range v.folders {
			list = append(list, map[string]any{"id": id, "name": name})
		}
		ok(w, map[string]any{"object": "list", "data": list})
	})
	mux.HandleFunc("POST /object/folder", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !decodeBodyInternal(t, w, r, &body) {
			return
		}
		v.mu.Lock()
		v.nextID++
		id := fmt.Sprintf("folder-%d", v.nextID)
		v.folders[id] = body["name"].(string)
		v.mu.Unlock()
		ok(w, map[string]any{"object": "folder", "id": id, "name": body["name"]})
	})
	mux.HandleFunc("GET /list/object/items", func(w http.ResponseWriter, r *http.Request) {
		v.mu.Lock()
		defer v.mu.Unlock()
		list := []map[string]any{}
		for _, item := range v.items {
			if item["folderId"] == r.URL.Query().Get("folderid") {
				list = append(list, item)
			}
		}
		ok(w, map[string]any{"object": "list", "data": list})
	})
	mux.HandleFunc("POST /object/item", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !decodeBodyInternal(t, w, r, &body) {
			return
		}
		v.mu.Lock()
		v.nextID++
		body["id"] = fmt.Sprintf("item-%d", v.nextID)
		v.items[body["id"].(string)] = body
		v.mu.Unlock()
		ok(w, body)
	})
	mux.HandleFunc("GET /object/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		v.mu.Lock()
		defer v.mu.Unlock()
		ok(w, v.items[r.PathValue("id")])
	})
	mux.HandleFunc("PUT /object/item/{id}", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !decodeBodyInternal(t, w, r, &body) {
			return
		}
		v.mu.Lock()
		v.items[r.PathValue("id")] = body
		v.mu.Unlock()
		ok(w, body)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func (v *bwVaultInternal) valuesIn(folderID string) map[string]string {
	v.mu.Lock()
	defer v.mu.Unlock()
	values := map[string]string{}
	for _, item := range v.items {
		if item["folderId"] == folderID {
			values[item["name"].(string)], _ = item["notes"].(string)
		}
	}
	return values
}

func newBitwardenSetupFixtureInternal(t *testing.T) (*SecretSourceService, *bwVaultInternal, *fakeProjectFilesInternal, *secretsourcetypes.Source, ProjectRef) {
	t.Helper()
	service, _ := setupSecretSourceServiceTestInternal(t)
	vault := &bwVaultInternal{folders: map[string]string{}, items: map[string]map[string]any{}}
	server := vault.server(t)
	files := &fakeProjectFilesInternal{files: ProjectFiles{ComposeContents: []string{setupComposeTest}, EnvContent: setupEnvTest}}
	service.SetProjectFileAccess(files)
	source, err := service.CreateSource(t.Context(), secretsourcetypes.CreateSourceRequest{
		Name:     "Vaultwarden",
		Provider: secretsourcetypes.ProviderBitwarden,
		Settings: secretsourcetypes.SourceSettings{Bitwarden: &secretsourcetypes.BitwardenSettings{ServeURL: server.URL}},
	})
	require.NoError(t, err)
	return service, vault, files, source, ProjectRef{ID: "project-1", Name: "My App"}
}

func TestBitwardenSetupNewFolder(t *testing.T) {
	service, vault, files, source, project := newBitwardenSetupFixtureInternal(t)

	plan, err := service.PlanSetup(t.Context(), project, secretsourcetypes.SetupPlanRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.SetupTarget{Mode: secretsourcetypes.SetupModeNewFolder},
	})
	require.NoError(t, err)
	assert.Equal(t, secretsourcetypes.ProviderBitwarden, plan.Provider)
	assert.True(t, plan.CanWrite)
	assert.Nil(t, plan.DeployIdentity)
	assert.Empty(t, plan.DeployIdentityError)
	assert.Equal(t, "My App", plan.SuggestedFolderName)
	assert.False(t, plan.ProjectNameTaken)

	result, err := service.ApplySetup(t.Context(), project, secretsourcetypes.SetupApplyRequest{
		SourceID:            source.ID,
		Target:              secretsourcetypes.SetupTarget{Mode: secretsourcetypes.SetupModeNewFolder},
		Keys:                []string{"DB_PASSWORD", "API_TOKEN"},
		Values:              secretsourcetypes.SetupValuesImport,
		EnvFile:             secretsourcetypes.SetupEnvFileRemove,
		GrantDeployIdentity: true,
		Required:            true,
	}, usertypes.SystemUser)
	require.NoError(t, err)
	require.True(t, result.OK, "%+v", result.Steps)
	assert.Equal(t, map[string]string{
		"folder": "done", "secrets": "done", "grant": "skipped", "binding": "done", "verify": "done", "envFile": "done",
	}, stepStatusesInternal(result))

	require.NotNil(t, result.Binding)
	require.NotNil(t, result.Binding.Target.Bitwarden)
	folderID := result.Binding.Target.Bitwarden.ID
	assert.Equal(t, "My App", vault.folders[folderID])
	assert.Equal(t, secretsourcetypes.BitwardenScopeFolder, result.Binding.Target.Bitwarden.Scope)
	assert.True(t, result.Binding.Required)
	assert.Equal(t, map[string]string{"DB_PASSWORD": "hunter2", "API_TOKEN": "local-token"}, vault.valuesIn(folderID))
	assert.Equal(t, []string{"API_TOKEN", "DB_PASSWORD"}, result.RemovedKeys)
	assert.NotContains(t, files.files.EnvContent, "hunter2")

	// Running it again must not create a second folder with the same name.
	_, err = service.ApplySetup(t.Context(), project, secretsourcetypes.SetupApplyRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.SetupTarget{Mode: secretsourcetypes.SetupModeNewFolder},
		Keys:     []string{"TZ"},
		Values:   secretsourcetypes.SetupValuesImport,
		EnvFile:  secretsourcetypes.SetupEnvFileKeep,
	}, usertypes.SystemUser)
	require.Error(t, err)
	assert.Len(t, vault.folders, 1)
}

func TestBitwardenSetupExistingFolderOverwritesOnlyWhenAsked(t *testing.T) {
	service, vault, _, source, project := newBitwardenSetupFixtureInternal(t)
	vault.folders["f1"] = "apps"
	vault.items["i1"] = map[string]any{"id": "i1", "type": 2, "name": "API_TOKEN", "notes": "rotated", "folderId": "f1", "secureNote": map[string]any{"type": 0}}

	plan, err := service.PlanSetup(t.Context(), project, secretsourcetypes.SetupPlanRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.SetupTarget{Mode: secretsourcetypes.SetupModeExistingFolder, FolderID: "f1", FolderName: "apps"},
	})
	require.NoError(t, err)
	remote := map[string]string{}
	for _, variable := range plan.Variables {
		remote[variable.Key] = variable.Remote
	}
	assert.Equal(t, secretsourcetypes.SetupRemoteDifferent, remote["API_TOKEN"])
	assert.Equal(t, secretsourcetypes.SetupRemoteMissing, remote["DB_PASSWORD"])

	request := secretsourcetypes.SetupApplyRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.SetupTarget{Mode: secretsourcetypes.SetupModeExistingFolder, FolderID: "f1", FolderName: "apps"},
		Keys:     []string{"API_TOKEN", "DB_PASSWORD"},
		Values:   secretsourcetypes.SetupValuesImport,
		EnvFile:  secretsourcetypes.SetupEnvFileKeep,
	}
	result, err := service.ApplySetup(t.Context(), project, request, usertypes.SystemUser)
	require.NoError(t, err)
	require.True(t, result.OK, "%+v", result.Steps)
	assert.Equal(t, map[string]string{"API_TOKEN": "rotated", "DB_PASSWORD": "hunter2"}, vault.valuesIn("f1"))
	assert.Equal(t, "apps", result.Binding.Target.Bitwarden.Name)

	request.OverwriteKeys = []string{"API_TOKEN"}
	result, err = service.ApplySetup(t.Context(), project, request, usertypes.SystemUser)
	require.NoError(t, err)
	require.True(t, result.OK, "%+v", result.Steps)
	assert.Equal(t, map[string]string{"API_TOKEN": "local-token", "DB_PASSWORD": "hunter2"}, vault.valuesIn("f1"))
	assert.Len(t, vault.items, 2, "no duplicate items")
}

func TestTargetKeysReturnsNamesOnly(t *testing.T) {
	service, vault, _, source, _ := newBitwardenSetupFixtureInternal(t)
	vault.folders["f1"] = "apps"
	vault.items["i1"] = map[string]any{"id": "i1", "type": 2, "name": "API_TOKEN", "notes": "secret", "folderId": "f1"}
	vault.items["i2"] = map[string]any{"id": "i2", "type": 2, "name": "bad key", "notes": "x", "folderId": "f1"}
	vault.items["i3"] = map[string]any{"id": "i3", "type": 1, "name": "EMPTY", "login": map[string]any{"password": nil}, "folderId": "f1"}

	keys, err := service.TargetKeys(t.Context(), source.ID, secretsourcetypes.BindingTarget{
		Bitwarden: &secretsourcetypes.BitwardenTarget{Scope: secretsourcetypes.BitwardenScopeFolder, ID: "f1"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"API_TOKEN"}, keys.Keys)
	assert.Equal(t, []string{"EMPTY", "bad key"}, keys.Skipped)

	_, err = service.TargetKeys(t.Context(), source.ID, secretsourcetypes.BindingTarget{})
	require.Error(t, err)
}

func TestAddComposeRefsWrapsInjector(t *testing.T) {
	result, err := AddComposeRefs(secretsourcetypes.ComposeRefsRequest{
		Compose:     "services:\n  app:\n    image: x\n",
		Assignments: map[string][]string{"app": {"API_TOKEN"}, "nope": {"X"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "services:\n  app:\n    environment:\n      API_TOKEN: ${API_TOKEN}\n    image: x\n", result.Compose)
	assert.Equal(t, map[string][]string{"app": {"API_TOKEN"}}, result.Added)
	require.Len(t, result.Skipped, 1)

	_, err = AddComposeRefs(secretsourcetypes.ComposeRefsRequest{Compose: "services: ["})
	require.Error(t, err)
	services, err := ComposeServices("services:\n  app:\n    image: x\n")
	require.NoError(t, err)
	assert.Equal(t, []secretsourcetypes.ComposeService{{Name: "app", Available: []string{}, Editable: true}}, services)
}

func TestBitwardenSetupPlaceholdersVerify(t *testing.T) {
	service, vault, files, source, project := newBitwardenSetupFixtureInternal(t)

	result, err := service.ApplySetup(t.Context(), project, secretsourcetypes.SetupApplyRequest{
		SourceID: source.ID,
		Target:   secretsourcetypes.SetupTarget{Mode: secretsourcetypes.SetupModeNewFolder, FolderName: "placeholders"},
		Keys:     []string{"NEW_SECRET", "API_TOKEN"},
		Values:   secretsourcetypes.SetupValuesPlaceholder,
		EnvFile:  secretsourcetypes.SetupEnvFileKeep,
	}, usertypes.SystemUser)
	require.NoError(t, err)
	require.True(t, result.OK, "empty notes are skipped on read but still count as written: %+v", result.Steps)
	folderID := result.Binding.Target.Bitwarden.ID
	assert.Equal(t, map[string]string{"NEW_SECRET": "", "API_TOKEN": ""}, vault.valuesIn(folderID))
	assert.Equal(t, setupEnvTest, files.files.EnvContent)
}
