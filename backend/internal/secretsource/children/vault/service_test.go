package vault

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeTargetDefaultsAndRejectsTraversal(t *testing.T) {
	target, err := NormalizeTarget(&secretsourcetypes.VaultTarget{Path: " /apps/immich/ "})
	require.NoError(t, err)
	assert.Equal(t, &secretsourcetypes.VaultTarget{Mount: "secret", Path: "apps/immich", KVVersion: 2}, target)

	for _, bad := range []secretsourcetypes.VaultTarget{
		{Path: ""},
		{Path: "apps/../sys"},
		{Path: "apps//x"},
		{Mount: "..", Path: "x"},
		{Path: "x", KVVersion: 3},
	} {
		_, err := NormalizeTarget(&bad)
		assert.Error(t, err, bad)
	}
}

func TestNormalizeSettings(t *testing.T) {
	settings, err := NormalizeSettings(&secretsourcetypes.VaultSettings{Address: "http://openbao:8200/", Namespace: "/team/"})
	require.NoError(t, err)
	assert.Equal(t, &secretsourcetypes.VaultSettings{Address: "http://openbao:8200", Namespace: "team", AuthMethod: "token"}, settings)

	settings, err = NormalizeSettings(&secretsourcetypes.VaultSettings{Address: "http://v:8200", AuthMethod: "approle", RoleID: "r"})
	require.NoError(t, err)
	assert.Equal(t, "approle", settings.AppRoleMount)

	_, err = NormalizeSettings(&secretsourcetypes.VaultSettings{Address: "http://v:8200", AuthMethod: "approle"})
	assert.Error(t, err)
}

func TestFetchSkipsNonScalarValues(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/secret/data/app", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"data":{"DB":"pw","PORT":5432,"NESTED":{"a":1}},"metadata":{"version":1}}}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	provider, err := New(server.Client(), secretsourcetypes.VaultSettings{Address: server.URL, AuthMethod: "token"}, "t")
	require.NoError(t, err)

	values, skipped, err := provider.Fetch(t.Context(), secretsourcetypes.BindingTarget{Vault: &secretsourcetypes.VaultTarget{Mount: "secret", Path: "app", KVVersion: 2}})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"DB": "pw", "PORT": "5432"}, values)
	assert.Equal(t, []string{"NESTED"}, skipped)

	_, _, err = provider.Fetch(t.Context(), secretsourcetypes.BindingTarget{Vault: &secretsourcetypes.VaultTarget{Mount: "secret", Path: "missing", KVVersion: 2}})
	assert.ErrorContains(t, err, "no secret at secret/missing")
}

// fakeKVInternal is a tiny KV v2 store with check-and-set.
type fakeKVInternal struct {
	mu      sync.Mutex
	data    map[string]string
	version int
	writes  []string
}

func (f *fakeKVInternal) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/secret/data/arcane/web", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "setup-token", r.Header.Get("X-Vault-Token"))
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.data == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}
		body, err := json.Marshal(map[string]any{"data": map[string]any{"data": f.data, "metadata": map[string]int{"version": f.version}}})
		require.NoError(t, err)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("POST /v1/secret/data/arcane/web", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.writes = append(f.writes, string(raw))
		_, _ = w.Write([]byte(`{"data":{"version":1}}`))
	})
	return mux
}

func TestSetupMergesWithExistingKeysAndSendsCAS(t *testing.T) {
	kv := &fakeKVInternal{data: map[string]string{"KEEP": "k", "OLD": "o"}, version: 2}
	server := httptest.NewServer(kv.handler(t))
	t.Cleanup(server.Close)

	setup, err := NewSetup(server.Client(), secretsourcetypes.VaultSettings{Address: server.URL, AuthMethod: "token", SetupToken: true}, "setup-token")
	require.NoError(t, err)
	target, err := NormalizeSetupTarget(secretsourcetypes.SetupTarget{Mode: secretsourcetypes.SetupModeKVPath}, "Web")
	require.NoError(t, err)
	assert.Equal(t, "arcane/web", target.SecretPath)

	remote, err := setup.RemoteValues(t.Context(), target)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"KEEP": "k", "OLD": "o"}, remote)

	err = setup.Write(t.Context(), target, map[string]string{"NEW": "n", "OLD": "o2"}, []string{"NEW"}, []string{"OLD"})
	require.NoError(t, err)
	require.Len(t, kv.writes, 1)
	assert.JSONEq(t, `{"data":{"KEEP":"k","NEW":"n","OLD":"o2"},"options":{"cas":2}}`, kv.writes[0])

	err = setup.Write(t.Context(), target, map[string]string{"KEEP": "x"}, []string{"KEEP"}, nil)
	assert.ErrorContains(t, err, "already exists")
}

func TestSetupNeedsSetupToken(t *testing.T) {
	_, err := NewSetup(nil, secretsourcetypes.VaultSettings{Address: "http://v:8200"}, "tok")
	assert.ErrorIs(t, err, ErrNoSetupToken)
	_, err = NewSetup(nil, secretsourcetypes.VaultSettings{Address: "http://v:8200", SetupToken: true}, "")
	assert.ErrorIs(t, err, ErrNoSetupToken)
}

func TestSuggestPath(t *testing.T) {
	assert.Equal(t, "arcane/my-app", SuggestPath("My App!"))
	assert.Equal(t, "arcane/project", SuggestPath("!!!"))
}
