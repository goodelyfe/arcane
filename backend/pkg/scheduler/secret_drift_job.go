package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	schedulertypes "github.com/getarcaneapp/arcane/types/v2/scheduler"
	usertypes "github.com/getarcaneapp/arcane/types/v2/user"

	"github.com/getarcaneapp/arcane/backend/v2/internal/project"
	"github.com/getarcaneapp/arcane/backend/v2/internal/secretsource"
)

// SecretDriftCheckJobName identifies the background check of bound project
// secrets against what each project last deployed with.
const SecretDriftCheckJobName = "secret-drift-check"

// SecretDriftCheckJob fetches every deployed project's bound secrets, records
// whether they changed since the last deploy, and redeploys running projects
// whose binding opts in to auto-redeploy. Each change triggers at most one
// redeploy attempt. Internal job: no job_metadata entry.
type SecretDriftCheckJob struct {
	secrets  *secretsource.SecretSourceService
	projects *project.ProjectService
}

func NewSecretDriftCheckJob(secrets *secretsource.SecretSourceService, projects *project.ProjectService) *SecretDriftCheckJob {
	return &SecretDriftCheckJob{secrets: secrets, projects: projects}
}

func (j *SecretDriftCheckJob) Name() string {
	return SecretDriftCheckJobName
}

func (j *SecretDriftCheckJob) Schedule(_ context.Context) string {
	// Every five minutes, offset from jobs that fire on the minute.
	return "20 */5 * * * *"
}

func (j *SecretDriftCheckJob) Run(ctx context.Context) (schedulertypes.Outcome, error) {
	if j.secrets == nil || j.projects == nil {
		return schedulertypes.Outcome{Status: schedulertypes.Skipped}, nil
	}

	changes := j.secrets.CheckDrift(ctx, j.projects.SecretProjectRef)
	var errs []error
	for _, change := range changes {
		if !change.AutoRedeploy {
			continue
		}
		proj, err := j.projects.GetProjectFromDatabaseByID(ctx, change.ProjectID)
		if err != nil {
			errs = append(errs, fmt.Errorf("project %s: %w", change.ProjectID, err))
			continue
		}
		if proj.Status != project.ProjectStatusRunning {
			slog.InfoContext(ctx, "Secrets changed for a project that is not running; not redeploying",
				"jobName", SecretDriftCheckJobName, "projectId", change.ProjectID, "status", string(proj.Status))
			continue
		}
		slog.InfoContext(ctx, "Redeploying project after its secrets changed", "jobName", SecretDriftCheckJobName, "projectId", change.ProjectID)
		if deployErr := j.projects.DeployProject(ctx, change.ProjectID, usertypes.SystemUser, nil); deployErr != nil {
			errs = append(errs, fmt.Errorf("redeploy %s: %w", change.ProjectName, deployErr))
		}
	}

	if joined := errors.Join(errs...); joined != nil {
		slog.WarnContext(ctx, "Secret drift check finished with errors", "jobName", SecretDriftCheckJobName, "error", joined)
		return schedulertypes.Outcome{Status: schedulertypes.Partial}, joined
	}
	return schedulertypes.Outcome{Status: schedulertypes.Succeeded}, nil
}

func (j *SecretDriftCheckJob) Reschedule(_ context.Context) error {
	return nil
}
