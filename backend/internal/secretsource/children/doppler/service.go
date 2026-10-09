// Package doppler is the Doppler secret provider. A binding reads one config;
// each secret is one variable.
package doppler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"

	"github.com/getarcaneapp/arcane/backend/v2/pkg/doppler"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/secretapi"
)

// Provider reads secrets with one Doppler token.
type Provider struct {
	client *doppler.Client
}

// NormalizeSettings validates settings and returns them in canonical form.
// The public API URL is stored as empty.
func NormalizeSettings(settings *secretsourcetypes.DopplerSettings) (*secretsourcetypes.DopplerSettings, error) {
	if settings == nil {
		settings = &secretsourcetypes.DopplerSettings{}
	}
	apiURL, err := doppler.ParseAPIURL(settings.APIURL)
	if err != nil {
		return nil, err
	}
	normalized := &secretsourcetypes.DopplerSettings{}
	if apiURL.String() != doppler.DefaultAPIURL {
		normalized.APIURL = apiURL.String()
	}
	return normalized, nil
}

// NormalizeTarget validates a binding target. Project and config go together:
// both empty (service token) or both set.
func NormalizeTarget(target *secretsourcetypes.DopplerTarget) (*secretsourcetypes.DopplerTarget, error) {
	if target == nil {
		return &secretsourcetypes.DopplerTarget{}, nil
	}
	project := strings.TrimSpace(target.Project)
	config := strings.TrimSpace(target.Config)
	if (project == "") != (config == "") {
		return nil, errors.New("set both the Doppler project and config, or neither when the token is a service token")
	}
	return &secretsourcetypes.DopplerTarget{Project: project, Config: config}, nil
}

// Describe names a target in messages, e.g. config "prd" of project "api".
func Describe(target *secretsourcetypes.DopplerTarget) string {
	if target == nil || target.Project == "" {
		return "the service token's config"
	}
	return fmt.Sprintf("config %q of project %q", target.Config, target.Project)
}

// New builds a provider.
func New(httpClient *http.Client, settings secretsourcetypes.DopplerSettings, token string) (*Provider, error) {
	client, err := doppler.NewClient(httpClient, settings.APIURL, token)
	if err != nil {
		return nil, err
	}
	return &Provider{client: client}, nil
}

// Test checks the token and whether it can list projects.
func (p *Provider) Test(ctx context.Context) secretsourcetypes.TestSourceResult {
	info, err := p.client.Me(ctx)
	if err != nil {
		return secretsourcetypes.TestSourceResult{OK: false, Message: err.Error()}
	}
	who := info.Name
	if who == "" {
		who = "the token"
	}
	if info.Workplace.Name != "" {
		who += " in " + info.Workplace.Name
	}
	projects, listErr := p.client.Projects(ctx)
	if listErr != nil {
		// Service tokens are scoped to one config and cannot list.
		return secretsourcetypes.TestSourceResult{OK: true, Message: fmt.Sprintf("Authenticated as %s (%s); bind projects without picking a config", who, info.Type)}
	}
	return secretsourcetypes.TestSourceResult{
		OK:           true,
		Message:      fmt.Sprintf("Authenticated as %s (%s)", who, info.Type),
		CanBrowse:    true,
		VisibleCount: len(projects),
	}
}

// Browse lists projects, or the configs of a project.
func (p *Provider) Browse(ctx context.Context, query secretsourcetypes.BrowseQuery) ([]secretsourcetypes.BrowseItem, error) {
	switch query.Kind {
	case secretsourcetypes.BrowseDopplerProjects:
		projects, err := p.client.Projects(ctx)
		if err != nil {
			return nil, err
		}
		items := make([]secretsourcetypes.BrowseItem, 0, len(projects))
		for _, project := range projects {
			items = append(items, secretsourcetypes.BrowseItem{ID: project.Slug, Name: project.Name})
		}
		return sortedInternal(items), nil
	case secretsourcetypes.BrowseDopplerConfigs:
		if strings.TrimSpace(query.ProjectID) == "" {
			return nil, errors.New("a Doppler project is required to list configs")
		}
		configs, err := p.client.Configs(ctx, query.ProjectID)
		if err != nil {
			return nil, err
		}
		items := make([]secretsourcetypes.BrowseItem, 0, len(configs))
		for _, config := range configs {
			items = append(items, secretsourcetypes.BrowseItem{ID: config.Name, Name: config.Name, Detail: config.Environment})
		}
		return sortedInternal(items), nil
	default:
		return nil, fmt.Errorf("unknown browse kind %q for Doppler", query.Kind)
	}
}

// Fetch downloads the target config.
func (p *Provider) Fetch(ctx context.Context, target secretsourcetypes.BindingTarget) (map[string]string, []string, error) {
	if target.Doppler == nil {
		return nil, nil, errors.New("binding has no Doppler target")
	}
	secrets, err := p.client.Download(ctx, target.Doppler.Project, target.Doppler.Config)
	if err != nil {
		return nil, nil, err
	}
	values, skipped := secretapi.StringValues(secrets)
	slices.Sort(skipped)
	return values, skipped, nil
}

func sortedInternal(items []secretsourcetypes.BrowseItem) []secretsourcetypes.BrowseItem {
	slices.SortFunc(items, func(a, b secretsourcetypes.BrowseItem) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return items
}
