// Package vault is the HashiCorp Vault / OpenBao secret provider. A binding
// reads one secret of a KV mount; each key of the secret is one variable.
package vault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"

	"github.com/getarcaneapp/arcane/backend/v2/pkg/secretapi"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/vault"
)

// DefaultMount is the KV mount a dev-mode server creates.
const DefaultMount = "secret"

// Provider reads secrets from one Vault or OpenBao server.
type Provider struct {
	client *vault.Client
}

// NormalizeSettings validates settings and returns them in canonical form.
func NormalizeSettings(settings *secretsourcetypes.VaultSettings) (*secretsourcetypes.VaultSettings, error) {
	if settings == nil {
		return nil, errors.New("vault settings are required")
	}
	address, err := vault.ParseAddress(settings.Address)
	if err != nil {
		return nil, err
	}
	normalized := &secretsourcetypes.VaultSettings{
		Address:    address.String(),
		Namespace:  strings.Trim(strings.TrimSpace(settings.Namespace), "/"),
		AuthMethod: strings.TrimSpace(settings.AuthMethod),
	}
	switch normalized.AuthMethod {
	case "", secretsourcetypes.VaultAuthToken:
		normalized.AuthMethod = secretsourcetypes.VaultAuthToken
	case secretsourcetypes.VaultAuthAppRole:
		normalized.RoleID = strings.TrimSpace(settings.RoleID)
		if normalized.RoleID == "" {
			return nil, errors.New("an AppRole role ID is required")
		}
		normalized.AppRoleMount = strings.Trim(strings.TrimSpace(settings.AppRoleMount), "/")
		if normalized.AppRoleMount == "" {
			normalized.AppRoleMount = "approle"
		}
	default:
		return nil, fmt.Errorf("unknown auth method %q; use token or approle", settings.AuthMethod)
	}
	return normalized, nil
}

// NormalizeTarget validates a binding target and returns it in canonical form.
func NormalizeTarget(target *secretsourcetypes.VaultTarget) (*secretsourcetypes.VaultTarget, error) {
	if target == nil {
		return nil, errors.New("a KV mount and secret path are required")
	}
	mount := strings.Trim(strings.TrimSpace(target.Mount), "/")
	if mount == "" {
		mount = DefaultMount
	}
	path := strings.Trim(strings.TrimSpace(target.Path), "/")
	if path == "" {
		return nil, errors.New("a secret path is required, e.g. apps/immich")
	}
	if err := checkPathInternal(mount); err != nil {
		return nil, fmt.Errorf("invalid mount: %w", err)
	}
	if err := checkPathInternal(path); err != nil {
		return nil, fmt.Errorf("invalid secret path: %w", err)
	}
	version := target.KVVersion
	if version == 0 {
		version = 2
	}
	if version != 1 && version != 2 {
		return nil, fmt.Errorf("KV version must be 1 or 2, got %d", target.KVVersion)
	}
	return &secretsourcetypes.VaultTarget{Mount: mount, Path: path, KVVersion: version}, nil
}

// Describe names a target in messages, e.g. secret/apps/immich.
func Describe(target *secretsourcetypes.VaultTarget) string {
	if target == nil {
		return "no target"
	}
	return target.Mount + "/" + target.Path
}

// ClientConfig turns settings and the credential into a client config.
func ClientConfig(settings secretsourcetypes.VaultSettings, credential string) vault.Config {
	return vault.Config{
		Address:      settings.Address,
		Namespace:    settings.Namespace,
		AuthMethod:   settings.AuthMethod,
		RoleID:       settings.RoleID,
		AppRoleMount: settings.AppRoleMount,
		Secret:       credential,
	}
}

// New builds a provider.
func New(httpClient *http.Client, settings secretsourcetypes.VaultSettings, credential string) (*Provider, error) {
	client, err := vault.NewClient(httpClient, ClientConfig(settings, credential))
	if err != nil {
		return nil, err
	}
	return &Provider{client: client}, nil
}

// Test logs in and looks the token up.
func (p *Provider) Test(ctx context.Context) secretsourcetypes.TestSourceResult {
	info, err := p.client.LookupSelf(ctx)
	if err != nil {
		return secretsourcetypes.TestSourceResult{OK: false, Message: err.Error()}
	}
	name := info.DisplayName
	if name == "" {
		name = "the token"
	}
	message := fmt.Sprintf("Authenticated as %s", name)
	if len(info.Policies) > 0 {
		message += " with policies " + strings.Join(info.Policies, ", ")
	}
	mounts, mountsErr := p.client.KVMounts(ctx)
	if mountsErr != nil {
		return secretsourcetypes.TestSourceResult{OK: true, Message: message + "; mounts cannot be listed, so type the mount when binding"}
	}
	return secretsourcetypes.TestSourceResult{OK: true, Message: message, CanBrowse: true, VisibleCount: len(mounts)}
}

// Browse lists KV mounts, or the keys under a path of a mount.
func (p *Provider) Browse(ctx context.Context, query secretsourcetypes.BrowseQuery) ([]secretsourcetypes.BrowseItem, error) {
	switch query.Kind {
	case secretsourcetypes.BrowseVaultMounts:
		mounts, err := p.client.KVMounts(ctx)
		if err != nil {
			return nil, err
		}
		items := make([]secretsourcetypes.BrowseItem, 0, len(mounts))
		for _, mount := range mounts {
			items = append(items, secretsourcetypes.BrowseItem{ID: mount.Path, Name: mount.Path, Detail: "KV v" + mount.Version})
		}
		return sortedInternal(items), nil
	case secretsourcetypes.BrowseVaultPaths:
		mount := strings.Trim(query.Mount, "/")
		if mount == "" {
			mount = DefaultMount
		}
		version := 2
		if query.KVVersion == 1 {
			version = 1
		}
		prefix := strings.Trim(query.Path, "/")
		keys, err := p.client.List(ctx, mount, version, prefix)
		if err != nil {
			return nil, err
		}
		items := make([]secretsourcetypes.BrowseItem, 0, len(keys))
		for _, key := range keys {
			full := key
			if prefix != "" {
				full = prefix + "/" + key
			}
			detail := "secret"
			if strings.HasSuffix(key, "/") {
				detail = "folder"
			}
			items = append(items, secretsourcetypes.BrowseItem{ID: strings.TrimSuffix(full, "/"), Name: key, Detail: detail})
		}
		return sortedInternal(items), nil
	default:
		return nil, fmt.Errorf("unknown browse kind %q for Vault/OpenBao", query.Kind)
	}
}

// Fetch reads the target secret. Values that are objects, arrays, or null are
// skipped; numbers and booleans become their JSON text.
func (p *Provider) Fetch(ctx context.Context, target secretsourcetypes.BindingTarget) (map[string]string, []string, error) {
	if target.Vault == nil {
		return nil, nil, errors.New("binding has no Vault/OpenBao target")
	}
	data, _, err := p.client.Read(ctx, target.Vault.Mount, target.Vault.KVVersion, target.Vault.Path)
	if vault.IsNotFound(err) {
		return nil, nil, fmt.Errorf("no secret at %s", Describe(target.Vault))
	}
	if err != nil {
		return nil, nil, err
	}
	values, skipped := secretapi.StringValues(data)
	slices.Sort(skipped)
	return values, skipped, nil
}

// checkPathInternal rejects segments that would change which endpoint is
// called, such as "..".
func checkPathInternal(path string) error {
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "":
			return errors.New("empty path segment")
		case ".", "..":
			return fmt.Errorf("%q is not allowed in a path", segment)
		}
	}
	return nil
}

func sortedInternal(items []secretsourcetypes.BrowseItem) []secretsourcetypes.BrowseItem {
	slices.SortFunc(items, func(a, b secretsourcetypes.BrowseItem) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return items
}
