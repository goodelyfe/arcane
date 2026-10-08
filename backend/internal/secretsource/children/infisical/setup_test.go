package infisical

import (
	"testing"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSuggestFolder(t *testing.T) {
	assert.Equal(t, "/my-app", SuggestFolder(" My App! "))
	assert.Equal(t, "/immich_server", SuggestFolder("immich_server"))
	assert.Equal(t, "/app", SuggestFolder("***"))
}

func TestNormalizeSetupTarget(t *testing.T) {
	target, err := NormalizeSetupTarget(secretsourcetypes.SetupTarget{Mode: "new-project", Environment: " prod "}, "immich")
	require.NoError(t, err)
	assert.Equal(t, secretsourcetypes.SetupTarget{Mode: "new-project", ProjectName: "immich", Environment: "prod", SecretPath: "/"}, target)

	target, err = NormalizeSetupTarget(secretsourcetypes.SetupTarget{Mode: "shared-folder", ProjectID: "p1", Environment: "prod"}, "Ghost Blog")
	require.NoError(t, err)
	assert.Equal(t, "/ghost-blog", target.SecretPath)

	target, err = NormalizeSetupTarget(secretsourcetypes.SetupTarget{Mode: "shared-folder", ProjectID: "p1", Environment: "prod", SecretPath: "apps/ghost/"}, "ghost")
	require.NoError(t, err)
	assert.Equal(t, "/apps/ghost", target.SecretPath)

	target, err = NormalizeSetupTarget(secretsourcetypes.SetupTarget{Mode: "existing-project", ProjectID: "p1", Environment: "dev"}, "x")
	require.NoError(t, err)
	assert.Equal(t, "/", target.SecretPath)
	assert.Empty(t, target.ProjectName)

	for _, bad := range []secretsourcetypes.SetupTarget{
		{Mode: "new-project"},
		{Mode: "existing-project", Environment: "prod"},
		{Mode: "somewhere", Environment: "prod"},
		{Mode: "new-project", Environment: "prod", ProjectName: string(make([]byte, 65))},
	} {
		_, err = NormalizeSetupTarget(bad, "app")
		assert.Error(t, err, "%+v", bad)
	}
}

func TestNormalizeSettingsKeepsSeparateSetupIdentity(t *testing.T) {
	settings, err := NormalizeSettings(&secretsourcetypes.InfisicalSettings{ClientID: "deploy", SetupClientID: " setup "})
	require.NoError(t, err)
	assert.Equal(t, "setup", settings.SetupClientID)

	_, err = NormalizeSettings(&secretsourcetypes.InfisicalSettings{ClientID: "same", SetupClientID: "same"})
	require.Error(t, err)
}

func TestNewSetupRequiresIdentity(t *testing.T) {
	_, err := NewSetup(nil, secretsourcetypes.InfisicalSettings{ClientID: "deploy"}, "secret")
	require.ErrorIs(t, err, ErrNoSetupIdentity)
	_, err = NewSetup(nil, secretsourcetypes.InfisicalSettings{ClientID: "deploy", SetupClientID: "setup"}, "")
	require.ErrorIs(t, err, ErrNoSetupIdentity)
}
