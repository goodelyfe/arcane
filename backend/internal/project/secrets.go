package project

import (
	"context"
	"fmt"
	"log/slog"

	usertypes "github.com/getarcaneapp/arcane/types/v2/user"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/internal/secretsource"
)

// SecretEnvResolver supplies a project's bound secrets to container-creating
// operations. Implemented by secretsource.SecretSourceService.
type SecretEnvResolver interface {
	ResolveDeployEnv(ctx context.Context, project secretsource.ProjectRef, actor usertypes.Actor) (secretsource.DeployEnv, error)
	RecordDeployed(ctx context.Context, projectID, hash string)
}

// SetSecretEnvResolver enables deploy-time secret injection.
func (s *ProjectService) SetSecretEnvResolver(resolver SecretEnvResolver) {
	s.secretEnv = resolver
}

// SecretProjectRef describes a project for secret bindings, including the
// paths needed to report which .env keys a secret overrides.
func (s *ProjectService) SecretProjectRef(ctx context.Context, projectID string) (secretsource.ProjectRef, error) {
	proj, found, err := s.FindProjectByID(ctx, projectID)
	if err != nil {
		return secretsource.ProjectRef{}, fmt.Errorf("failed to get project: %w", err)
	}
	if !found {
		return secretsource.ProjectRef{}, common.ErrProjectNotFound
	}
	projectsDir, dirErr := s.GetProjectsDirectory(ctx)
	if dirErr != nil {
		slog.DebugContext(ctx, "projects directory unavailable for secret override check", "error", dirErr)
		projectsDir = ""
	}
	return secretsource.ProjectRef{ID: proj.ID, Name: proj.Name, Path: proj.Path, ProjectsDirectory: projectsDir}, nil
}

func (s *ProjectService) resolveSecretEnvInternal(ctx context.Context, proj *Project, actor usertypes.Actor) (secretsource.DeployEnv, error) {
	if s.secretEnv == nil || proj == nil {
		return secretsource.DeployEnv{}, nil
	}
	return s.secretEnv.ResolveDeployEnv(ctx, secretsource.ProjectRef{ID: proj.ID, Name: proj.Name}, actor)
}

func (s *ProjectService) recordSecretDeployInternal(ctx context.Context, projectID string, secretEnv secretsource.DeployEnv) {
	if s.secretEnv == nil || secretEnv.Hash == "" {
		return
	}
	s.secretEnv.RecordDeployed(ctx, projectID, secretEnv.Hash)
}
