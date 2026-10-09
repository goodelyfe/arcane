package httpsource

import (
	"net/http"
	"net/http/httptest"
	"testing"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeTarget(t *testing.T) {
	target, err := NormalizeTarget(&secretsourcetypes.HTTPTarget{Path: " /secrets/immich/ "})
	require.NoError(t, err)
	assert.Equal(t, "secrets/immich", target.Path)

	target, err = NormalizeTarget(nil)
	require.NoError(t, err)
	assert.Empty(t, target.Path)

	for _, bad := range []string{"a/../b", "a//b", "a?x=1", "a#b"} {
		_, err := NormalizeTarget(&secretsourcetypes.HTTPTarget{Path: bad})
		assert.Error(t, err, bad)
	}
}

func TestTestAndFetch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /secrets/web", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"A":"1","LIST":[1,2]}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	provider, err := New(server.Client(), secretsourcetypes.HTTPSettings{BaseURL: server.URL}, "")
	require.NoError(t, err)

	result := provider.Test(t.Context())
	assert.True(t, result.OK)
	assert.Contains(t, result.Message, "set a path")

	values, skipped, err := provider.Fetch(t.Context(), secretsourcetypes.BindingTarget{HTTP: &secretsourcetypes.HTTPTarget{Path: "secrets/web"}})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"A": "1"}, values)
	assert.Equal(t, []string{"LIST"}, skipped)
}
