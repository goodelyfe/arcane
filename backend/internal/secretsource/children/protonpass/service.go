// Package protonpass is the Proton Pass secret provider. Proton Pass
// encrypts everything client-side, so Arcane does not talk to Proton
// directly: a pass-kit sidecar runs Proton's pass-cli with a personal access
// token and serves vaults, items, and values as JSON. Two mappings are
// supported, as with 1Password:
//   - vault: each item is one variable; the title is the key and its login
//     password (or note, or first hidden field) is the value.
//   - item: each custom field of one item is one variable.
package protonpass

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"

	"github.com/getarcaneapp/arcane/backend/v2/pkg/httpsecrets"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/secretapi"
)

// Provider reads from one pass-kit.
type Provider struct {
	client *httpsecrets.Client
}

type kitVault struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type kitItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// NormalizeSettings validates settings and returns them in canonical form.
func NormalizeSettings(settings *secretsourcetypes.ProtonPassSettings) (*secretsourcetypes.ProtonPassSettings, error) {
	if settings == nil {
		return nil, errors.New("settings for Proton Pass are required")
	}
	base, err := secretapi.ParseBaseURL(settings.KitURL, "the pass-kit URL", "http://pass-kit:8080")
	if err != nil {
		return nil, err
	}
	return &secretsourcetypes.ProtonPassSettings{KitURL: base.String()}, nil
}

// NormalizeTarget validates a binding target and returns it in canonical form.
func NormalizeTarget(target *secretsourcetypes.ProtonPassTarget) (*secretsourcetypes.ProtonPassTarget, error) {
	if target == nil {
		return nil, errors.New("a Proton Pass vault or item is required")
	}
	normalized := &secretsourcetypes.ProtonPassTarget{
		Scope:   strings.TrimSpace(target.Scope),
		VaultID: strings.TrimSpace(target.VaultID),
		Name:    strings.TrimSpace(target.Name),
	}
	if normalized.VaultID == "" {
		return nil, errors.New("a Proton Pass vault is required")
	}
	if err := checkSegmentInternal(normalized.VaultID); err != nil {
		return nil, err
	}
	switch normalized.Scope {
	case secretsourcetypes.OnePasswordScopeVault:
	case secretsourcetypes.OnePasswordScopeItem:
		normalized.ItemID = strings.TrimSpace(target.ItemID)
		if normalized.ItemID == "" {
			return nil, errors.New("a Proton Pass item is required")
		}
		if err := checkSegmentInternal(normalized.ItemID); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown Proton Pass scope %q; use vault or item", target.Scope)
	}
	return normalized, nil
}

// checkSegmentInternal rejects IDs that would change the kit path.
func checkSegmentInternal(id string) error {
	if strings.ContainsAny(id, "/?#") || id == "." || id == ".." {
		return fmt.Errorf("%q is not a Proton Pass ID", id)
	}
	return nil
}

// Describe names a target in messages, e.g. vault "Arcane".
func Describe(target *secretsourcetypes.ProtonPassTarget) string {
	if target == nil {
		return "no target"
	}
	name := target.Name
	if name == "" {
		name = target.VaultID
		if target.Scope == secretsourcetypes.OnePasswordScopeItem {
			name = target.ItemID
		}
	}
	return fmt.Sprintf("%s %q", target.Scope, name)
}

// New builds a provider. token is the kit's optional bearer token.
func New(httpClient *http.Client, settings secretsourcetypes.ProtonPassSettings, token string) (*Provider, error) {
	client, err := httpsecrets.NewClient(httpClient, settings.KitURL, token)
	if err != nil {
		return nil, err
	}
	return &Provider{client: client}, nil
}

func (p *Provider) vaultsInternal(ctx context.Context) ([]kitVault, error) {
	object, err := p.client.Get(ctx, "vaults")
	if err != nil {
		return nil, err
	}
	var vaults []kitVault
	if raw, ok := object["vaults"]; !ok || json.Unmarshal(raw, &vaults) != nil {
		return nil, fmt.Errorf("pass-kit: %w", secretapi.ErrUnexpectedShape)
	}
	return vaults, nil
}

// Test lists the vaults the token can read.
func (p *Provider) Test(ctx context.Context) secretsourcetypes.TestSourceResult {
	vaults, err := p.vaultsInternal(ctx)
	if err != nil {
		return secretsourcetypes.TestSourceResult{OK: false, Message: err.Error()}
	}
	return secretsourcetypes.TestSourceResult{
		OK:           true,
		Message:      fmt.Sprintf("Connected; the token can read %d vaults", len(vaults)),
		CanBrowse:    true,
		VisibleCount: len(vaults),
	}
}

// Browse lists vaults, or the items of a vault.
func (p *Provider) Browse(ctx context.Context, query secretsourcetypes.BrowseQuery) ([]secretsourcetypes.BrowseItem, error) {
	var items []secretsourcetypes.BrowseItem
	switch query.Kind {
	case secretsourcetypes.BrowseProtonPassVaults:
		vaults, err := p.vaultsInternal(ctx)
		if err != nil {
			return nil, err
		}
		for _, vault := range vaults {
			items = append(items, secretsourcetypes.BrowseItem{ID: vault.ID, Name: vault.Name})
		}
	case secretsourcetypes.BrowseProtonPassItems:
		vaultID := strings.TrimSpace(query.VaultID)
		if vaultID == "" {
			return nil, errors.New("a Proton Pass vault is required to list items")
		}
		if err := checkSegmentInternal(vaultID); err != nil {
			return nil, err
		}
		object, err := p.client.Get(ctx, "vaults/"+vaultID+"/items")
		if err != nil {
			return nil, err
		}
		var kitItems []kitItem
		if raw, ok := object["items"]; !ok || json.Unmarshal(raw, &kitItems) != nil {
			return nil, fmt.Errorf("pass-kit: %w", secretapi.ErrUnexpectedShape)
		}
		for _, item := range kitItems {
			items = append(items, secretsourcetypes.BrowseItem{ID: item.ID, Name: item.Title, Detail: item.Type})
		}
	default:
		return nil, fmt.Errorf("unknown browse kind %q for Proton Pass", query.Kind)
	}
	slices.SortFunc(items, func(a, b secretsourcetypes.BrowseItem) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
	})
	return items, nil
}

// Fetch reads the target vault or item. The kit does the mapping and
// returns entries it cannot map as null, which become skipped names.
func (p *Provider) Fetch(ctx context.Context, target secretsourcetypes.BindingTarget) (map[string]string, []string, error) {
	if target.ProtonPass == nil {
		return nil, nil, errors.New("binding has no Proton Pass target")
	}
	t := target.ProtonPass
	path := "secrets/" + t.VaultID
	if t.Scope == secretsourcetypes.OnePasswordScopeItem {
		path += "/" + t.ItemID
	}
	object, err := p.client.Get(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	values, skipped := secretapi.StringValues(object)
	slices.Sort(skipped)
	return values, skipped, nil
}
