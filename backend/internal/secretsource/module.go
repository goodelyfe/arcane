// Package secretsource owns connections to external secret providers
// (Infisical, Bitwarden/Vaultwarden), project secret bindings, the deploy-time
// secret fetch, and background drift checks.
package secretsource

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/getarcaneapp/arcane/backend/v2/internal/config"
	"github.com/getarcaneapp/arcane/backend/v2/internal/middleware"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/authz"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/utils/handlerutil"
)

type Module struct {
	service *SecretSourceService
	handler *SecretSourceHandler
}

func New(service *SecretSourceService, resolveProject ProjectResolver) *Module {
	return &Module{
		service: service,
		handler: &SecretSourceHandler{service: service, resolveProject: resolveProject},
	}
}

func (m *Module) Service() *SecretSourceService {
	if m == nil {
		return nil
	}
	return m.service
}

// RegisterRoutes registers manager routes. Agents do not serve them: a remote
// environment's bindings are not supported yet.
func (m *Module) RegisterRoutes(api huma.API, cfg *config.Config) {
	if cfg != nil && cfg.AgentMode {
		return
	}
	if m == nil {
		RegisterSecretSources(api, &SecretSourceHandler{})
		return
	}
	RegisterSecretSources(api, m.handler)
}

func RegisterSecretSources(api huma.API, h *SecretSourceHandler) {
	tags := []string{"Secret Sources"}

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "listSecretSources",
		Method:      "GET",
		Path:        "/secret-sources",
		Summary:     "List secret sources",
		Description: "List connections to external secret managers (client secrets are never returned)",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesList, h.ListSources)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "createSecretSource",
		Method:      "POST",
		Path:        "/secret-sources",
		Summary:     "Create a secret source",
		Description: "Save a connection to a secret provider: Infisical (Universal Auth) or Bitwarden/Vaultwarden (through bw serve)",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesCreate, h.CreateSource)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "testSecretSource",
		Method:      "POST",
		Path:        "/secret-sources/test",
		Summary:     "Test secret source settings",
		Description: "Log in with the given settings without saving them; pass sourceId to reuse a stored client secret",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesTest, h.TestSource)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "getSecretSource",
		Method:      "GET",
		Path:        "/secret-sources/{id}",
		Summary:     "Get a secret source",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesRead, h.GetSource)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "updateSecretSource",
		Method:      "PUT",
		Path:        "/secret-sources/{id}",
		Summary:     "Update a secret source",
		Description: "Update a secret source; an empty client secret keeps the stored one",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesUpdate, h.UpdateSource)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "deleteSecretSource",
		Method:      "DELETE",
		Path:        "/secret-sources/{id}",
		Summary:     "Delete a secret source",
		Description: "Delete a secret source that no project is bound to",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesDelete, h.DeleteSource)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "browseSecretSource",
		Method:      "GET",
		Path:        "/secret-sources/{id}/browse",
		Summary:     "Browse a secret source",
		Description: "List pickable binding targets, such as Infisical projects and folders or Bitwarden folders, collections, and items",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesRead, h.Browse)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "listSecretSourceTargetKeys",
		Method:      "POST",
		Path:        "/secret-sources/{id}/keys",
		Summary:     "List a target's variable names",
		Description: "Fetch a binding target and return its variable names, without values, to prepare a project before binding it",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesRead, h.TargetKeys)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "listComposeServicesForSecrets",
		Method:      "POST",
		Path:        "/secret-sources/compose/services",
		Summary:     "List compose services for secret references",
		Description: "List the services of compose content and the variables each already receives. Nothing is saved.",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesRead, h.ComposeServices)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "addComposeSecretRefs",
		Method:      "POST",
		Path:        "/secret-sources/compose/refs",
		Summary:     "Add secret references to compose content",
		Description: "Add KEY: ${KEY} entries to the environment of chosen services, keeping comments and layout. Returns the new content; nothing is saved.",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermSecretSourcesRead, h.AddComposeRefs)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "getProjectSecretBinding",
		Method:      "GET",
		Path:        "/environments/{id}/projects/{projectId}/secrets",
		Summary:     "Get a project's secret binding",
		Description: "Get the secret source binding of a project, or null when it has none",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermProjectsRead, h.GetProjectBinding)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "upsertProjectSecretBinding",
		Method:      "PUT",
		Path:        "/environments/{id}/projects/{projectId}/secrets",
		Summary:     "Bind a project to a secret source",
		Description: "Create or replace the project's secret binding; secrets are fetched at deploy time and never written to disk",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermProjectsUpdate, h.UpsertProjectBinding)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "deleteProjectSecretBinding",
		Method:      "DELETE",
		Path:        "/environments/{id}/projects/{projectId}/secrets",
		Summary:     "Remove a project's secret binding",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermProjectsUpdate, h.DeleteProjectBinding)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "checkProjectSecretBinding",
		Method:      "POST",
		Path:        "/environments/{id}/projects/{projectId}/secrets/check",
		Summary:     "Check a project's secrets",
		Description: "Fetch the bound secrets now and report key names, overridden .env keys, and whether a redeploy is needed. Values are never returned.",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
	}, authz.PermProjectsDeploy, h.CheckProjectBinding)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "planProjectSecretSetup",
		Method:      "POST",
		Path:        "/environments/{id}/projects/{projectId}/secrets/setup/plan",
		Summary:     "Plan moving a project's variables to a secret manager",
		Description: "List the variables the project's compose files and .env use and, for a target, where each stands in Infisical. Writes nothing; values are never returned.",
		Tags:        tags,
		Security:    handlerutil.DefaultOperationSecurity(),
		// Setup writes with the source's setup identity, so it also needs the
		// right to change secret sources.
		Middlewares: middleware.RequirePermission(api, authz.PermSecretSourcesUpdate),
	}, authz.PermProjectsUpdate, h.PlanProjectSetup)

	middleware.RegisterWithPermission(api, huma.Operation{
		OperationID: "applyProjectSecretSetup",
		Method:      "POST",
		Path:        "/environments/{id}/projects/{projectId}/secrets/setup",
		Summary:     "Move a project's variables to a secret manager",
		Description: "With the source's setup identity, create the Infisical project or folder and the secrets, " +
			"give the deploy identity read access, bind the project, verify the binding, " +
			"and optionally remove the moved keys from the .env.",
		Tags:     tags,
		Security: handlerutil.DefaultOperationSecurity(),
		// Setup writes with the source's setup identity, so it also needs the
		// right to change secret sources.
		Middlewares: middleware.RequirePermission(api, authz.PermSecretSourcesUpdate),
	}, authz.PermProjectsUpdate, h.ApplyProjectSetup)
}
