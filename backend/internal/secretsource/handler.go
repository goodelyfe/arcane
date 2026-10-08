package secretsource

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"
	"github.com/getarcaneapp/arcane/types/v2/base"
	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/utils/handlerutil"
)

// SecretSourceHandler translates HTTP input for secret sources and project
// secret bindings into service calls.
type SecretSourceHandler struct {
	service        *SecretSourceService
	resolveProject ProjectResolver
}

type ListSourcesInput struct{}

type GetSourceInput struct {
	ID string `path:"id" doc:"Secret source ID"`
}

type CreateSourceInput struct {
	Body secretsourcetypes.CreateSourceRequest
}

type UpdateSourceInput struct {
	ID   string `path:"id" doc:"Secret source ID"`
	Body secretsourcetypes.UpdateSourceRequest
}

type DeleteSourceInput struct {
	ID string `path:"id" doc:"Secret source ID"`
}

type TestSourceInput struct {
	Body secretsourcetypes.TestSourceRequest
}

type BrowseSourceInput struct {
	ID          string `path:"id" doc:"Secret source ID"`
	Kind        string `query:"kind" doc:"What to list: projects or folders (Infisical); folders, collections, or items (Bitwarden)"`
	ProjectID   string `query:"projectId" doc:"Infisical project ID, for folders"`
	Environment string `query:"environment" doc:"Infisical environment slug, for folders"`
	Path        string `query:"path" doc:"Infisical folder path to list" default:"/"`
}

type ProjectBindingInput struct {
	EnvironmentID string `path:"id" doc:"Environment ID"`
	ProjectID     string `path:"projectId" doc:"Project ID"`
}

type UpsertProjectBindingInput struct {
	EnvironmentID string `path:"id" doc:"Environment ID"`
	ProjectID     string `path:"projectId" doc:"Project ID"`
	Body          secretsourcetypes.UpsertBindingRequest
}

type SetupPlanInput struct {
	EnvironmentID string `path:"id" doc:"Environment ID"`
	ProjectID     string `path:"projectId" doc:"Project ID"`
	Body          secretsourcetypes.SetupPlanRequest
}

type SetupApplyInput struct {
	EnvironmentID string `path:"id" doc:"Environment ID"`
	ProjectID     string `path:"projectId" doc:"Project ID"`
	Body          secretsourcetypes.SetupApplyRequest
}

type ComposeServicesInput struct {
	Body secretsourcetypes.ComposeServicesRequest
}

type ComposeRefsInput struct {
	Body secretsourcetypes.ComposeRefsRequest
}

type TargetKeysInput struct {
	ID   string `path:"id" doc:"Secret source ID"`
	Body secretsourcetypes.TargetKeysRequest
}

func (h *SecretSourceHandler) ListSources(ctx context.Context, _ *ListSourcesInput) (*handlerutil.Out[[]secretsourcetypes.Source], error) {
	sources, err := h.service.ListSources(ctx)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(sources), nil
}

func (h *SecretSourceHandler) GetSource(ctx context.Context, input *GetSourceInput) (*handlerutil.Out[*secretsourcetypes.Source], error) {
	source, err := h.service.GetSource(ctx, input.ID)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(source), nil
}

func (h *SecretSourceHandler) CreateSource(ctx context.Context, input *CreateSourceInput) (*handlerutil.Out[*secretsourcetypes.Source], error) {
	source, err := h.service.CreateSource(ctx, input.Body)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(source), nil
}

func (h *SecretSourceHandler) UpdateSource(ctx context.Context, input *UpdateSourceInput) (*handlerutil.Out[*secretsourcetypes.Source], error) {
	source, err := h.service.UpdateSource(ctx, input.ID, input.Body)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(source), nil
}

func (h *SecretSourceHandler) DeleteSource(ctx context.Context, input *DeleteSourceInput) (*handlerutil.Out[base.MessageResponse], error) {
	if err := h.service.DeleteSource(ctx, input.ID); err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(base.MessageResponse{Message: "Secret source deleted"}), nil
}

func (h *SecretSourceHandler) TestSource(ctx context.Context, input *TestSourceInput) (*handlerutil.Out[secretsourcetypes.TestSourceResult], error) {
	result, err := h.service.TestSource(ctx, input.Body)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(result), nil
}

func (h *SecretSourceHandler) Browse(ctx context.Context, input *BrowseSourceInput) (*handlerutil.Out[[]secretsourcetypes.BrowseItem], error) {
	items, err := h.service.Browse(ctx, input.ID, secretsourcetypes.BrowseQuery{
		Kind:        input.Kind,
		ProjectID:   input.ProjectID,
		Environment: input.Environment,
		Path:        input.Path,
	})
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(items), nil
}

func (h *SecretSourceHandler) GetProjectBinding(ctx context.Context, input *ProjectBindingInput) (*handlerutil.Out[*secretsourcetypes.Binding], error) {
	if _, err := h.localProjectInternal(ctx, input.EnvironmentID, input.ProjectID); err != nil {
		return nil, err
	}
	binding, err := h.service.GetBinding(ctx, input.ProjectID)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(binding), nil
}

func (h *SecretSourceHandler) UpsertProjectBinding(ctx context.Context, input *UpsertProjectBindingInput) (*handlerutil.Out[*secretsourcetypes.Binding], error) {
	if _, err := h.localProjectInternal(ctx, input.EnvironmentID, input.ProjectID); err != nil {
		return nil, err
	}
	binding, err := h.service.UpsertBinding(ctx, input.ProjectID, input.Body)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(binding), nil
}

func (h *SecretSourceHandler) DeleteProjectBinding(ctx context.Context, input *ProjectBindingInput) (*handlerutil.Out[base.MessageResponse], error) {
	if _, err := h.localProjectInternal(ctx, input.EnvironmentID, input.ProjectID); err != nil {
		return nil, err
	}
	if err := h.service.DeleteBinding(ctx, input.ProjectID); err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(base.MessageResponse{Message: "Secret binding removed"}), nil
}

func (h *SecretSourceHandler) CheckProjectBinding(ctx context.Context, input *ProjectBindingInput) (*handlerutil.Out[secretsourcetypes.CheckResult], error) {
	project, err := h.localProjectInternal(ctx, input.EnvironmentID, input.ProjectID)
	if err != nil {
		return nil, err
	}
	result, err := h.service.CheckBinding(ctx, project, handlerutil.CurrentActor(ctx))
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(result), nil
}

func (h *SecretSourceHandler) PlanProjectSetup(ctx context.Context, input *SetupPlanInput) (*handlerutil.Out[secretsourcetypes.SetupPlan], error) {
	project, err := h.localProjectInternal(ctx, input.EnvironmentID, input.ProjectID)
	if err != nil {
		return nil, err
	}
	plan, err := h.service.PlanSetup(ctx, project, input.Body)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(plan), nil
}

func (h *SecretSourceHandler) ApplyProjectSetup(ctx context.Context, input *SetupApplyInput) (*handlerutil.Out[secretsourcetypes.SetupResult], error) {
	project, err := h.localProjectInternal(ctx, input.EnvironmentID, input.ProjectID)
	if err != nil {
		return nil, err
	}
	result, err := h.service.ApplySetup(ctx, project, input.Body, handlerutil.CurrentActor(ctx))
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(result), nil
}

func (h *SecretSourceHandler) ComposeServices(_ context.Context, input *ComposeServicesInput) (*handlerutil.Out[[]secretsourcetypes.ComposeService], error) {
	services, err := ComposeServices(input.Body.Compose)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(services), nil
}

func (h *SecretSourceHandler) AddComposeRefs(_ context.Context, input *ComposeRefsInput) (*handlerutil.Out[secretsourcetypes.ComposeRefsResult], error) {
	result, err := AddComposeRefs(input.Body)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(result), nil
}

func (h *SecretSourceHandler) TargetKeys(ctx context.Context, input *TargetKeysInput) (*handlerutil.Out[secretsourcetypes.TargetKeys], error) {
	keys, err := h.service.TargetKeys(ctx, input.ID, input.Body.Target)
	if err != nil {
		return nil, httpErrorInternal(err)
	}
	return okInternal(keys), nil
}

// localProjectInternal resolves a project of the local environment. Remote
// environments resolve bindings on their own agent, which this version does
// not support yet.
func (h *SecretSourceHandler) localProjectInternal(ctx context.Context, environmentID, projectID string) (ProjectRef, error) {
	if environmentID != localEnvironmentID {
		return ProjectRef{}, huma.Error400BadRequest("Secret sources are currently supported on the local environment only")
	}
	if h.resolveProject == nil {
		return ProjectRef{}, huma.Error500InternalServerError("project lookup is not configured")
	}
	project, err := h.resolveProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return ProjectRef{}, huma.Error404NotFound(err.Error())
		}
		return ProjectRef{}, huma.Error500InternalServerError(err.Error())
	}
	return project, nil
}

func okInternal[T any](data T) *handlerutil.Out[T] {
	return &handlerutil.Out[T]{Body: base.ApiResponse[T]{Success: true, Data: data}}
}

func httpErrorInternal(err error) error {
	switch {
	case errors.Is(err, common.ErrValidation), errors.Is(err, common.ErrBadRequest):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, common.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, common.ErrConflict):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, common.ErrUnavailable):
		return huma.Error502BadGateway(err.Error())
	default:
		return huma.Error500InternalServerError(err.Error())
	}
}
