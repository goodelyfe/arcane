package secretsource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"
	usertypes "github.com/getarcaneapp/arcane/types/v2/user"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/internal/secretsource/children/infisical"
	"github.com/getarcaneapp/arcane/backend/v2/internal/secretsource/children/setupplan"
	infisicalclient "github.com/getarcaneapp/arcane/backend/v2/pkg/infisical"
	"github.com/getarcaneapp/arcane/backend/v2/pkg/projects"
)

// setupTimeout bounds one plan or apply, which make several provider calls.
const setupTimeout = 2 * time.Minute

const deployIdentityNotFound = "the setup identity cannot find the deploy identity; give it permission to read the " +
	"organization's identities, or add the deploy identity to the project in Infisical yourself"

// Setup step IDs, in the order they run.
const (
	setupStepProject = "project"
	setupStepFolder  = "folder"
	setupStepSecrets = "secrets"
	setupStepGrant   = "grant"
	setupStepBinding = "binding"
	setupStepVerify  = "verify"
	setupStepEnvFile = "envFile"
)

// ProjectFiles is what setup reads from a project.
type ProjectFiles struct {
	// ComposeContents are the compose file and its override, if any.
	ComposeContents []string
	// EnvContent is the effective .env.
	EnvContent string
	// GitEnvContent is the env file synced from Git, for Git projects.
	GitEnvContent string
	HasGitSource  bool
}

// EnvRemoval reports keys removed from a project's .env.
type EnvRemoval struct {
	Removed []string
	// Referenced stayed because other .env entries use them.
	Referenced []string
	// Changed stayed because their value changed after verification.
	Changed []string
	// BackupFile is relative to the project directory.
	BackupFile string
}

// ProjectFileAccess reads and edits project files for setup. The project
// service implements it, so file writes keep its validation, backups, and Git
// env layering.
type ProjectFileAccess interface {
	SecretSetupFiles(ctx context.Context, projectID string) (ProjectFiles, error)
	// RemoveSecretEnvKeys removes each key of expected whose current .env value
	// still equals the expected value.
	RemoveSecretEnvKeys(ctx context.Context, projectID string, expected map[string]string, keepBackup bool, actor usertypes.Actor) (EnvRemoval, error)
}

// SetProjectFileAccess wires the project service in after construction; the
// two services depend on each other.
func (s *SecretSourceService) SetProjectFileAccess(files ProjectFileAccess) {
	s.projectFiles = files
}

// setupContextInternal is the state shared by plan and apply.
type setupContextInternal struct {
	source    *SecretSource
	setup     *infisical.Setup
	variables []secretsourcetypes.SetupVariable
	envValues map[string]string
	files     ProjectFiles
}

func (s *SecretSourceService) loadSetupContextInternal(ctx context.Context, project ProjectRef, sourceID string) (*setupContextInternal, error) {
	if s.projectFiles == nil {
		return nil, errors.New("project file access is not configured")
	}
	source, err := s.loadSourceInternal(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if source.Provider != secretsourcetypes.ProviderInfisical || source.Settings.Infisical == nil {
		return nil, common.Classify(common.ErrSecretSourceInvalid, errors.New("project setup is available for Infisical sources"))
	}

	files, err := s.projectFiles.SecretSetupFiles(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	// Parser errors quote the text near the problem, which can be a secret,
	// so they are not passed on.
	envValues, err := projects.ParseProjectEnvContent(files.EnvContent, nil)
	if err != nil {
		return nil, common.Classify(common.ErrValidation, errors.New("could not parse the project's .env; check its syntax"))
	}
	gitKeys := map[string]struct{}{}
	if files.HasGitSource && strings.TrimSpace(files.GitEnvContent) != "" {
		gitValues, gitErr := projects.ParseProjectEnvContent(files.GitEnvContent, nil)
		if gitErr != nil {
			return nil, common.Classify(common.ErrValidation, errors.New("could not parse the project's Git env file; check its syntax"))
		}
		for key := range gitValues {
			gitKeys[key] = struct{}{}
		}
	}
	variables, err := setupplan.Analyze(setupplan.Inputs{ComposeFiles: files.ComposeContents, EnvValues: envValues, GitKeys: gitKeys})
	if err != nil {
		return nil, common.Classify(common.ErrValidation, err)
	}

	setupCtx := &setupContextInternal{source: source, variables: variables, envValues: envValues, files: files}
	setupSecret, err := decryptCredentialInternal(source.SetupCredential)
	if err != nil {
		return nil, err
	}
	setup, err := infisical.NewSetup(s.httpClient, *source.Settings.Infisical, setupSecret)
	switch {
	case errors.Is(err, infisical.ErrNoSetupIdentity):
	case err != nil:
		return nil, common.Classify(common.ErrSecretSourceInvalid, err)
	default:
		setupCtx.setup = setup
	}
	return setupCtx, nil
}

// PlanSetup lists the project's variables and, when a target is given, where
// each stands in Infisical. It writes nothing and returns no values.
func (s *SecretSourceService) PlanSetup(ctx context.Context, project ProjectRef, req secretsourcetypes.SetupPlanRequest) (secretsourcetypes.SetupPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()

	setupCtx, err := s.loadSetupContextInternal(ctx, project, req.SourceID)
	if err != nil {
		return secretsourcetypes.SetupPlan{}, err
	}
	existing, err := s.GetBinding(ctx, project.ID)
	if err != nil {
		return secretsourcetypes.SetupPlan{}, err
	}

	plan := secretsourcetypes.SetupPlan{
		SuggestedProjectName: project.Name,
		SuggestedSecretPath:  infisical.SuggestFolder(project.Name),
		CanWrite:             setupCtx.setup != nil,
		Variables:            setupCtx.variables,
		HasGitSource:         setupCtx.files.HasGitSource,
		AlreadyBound:         existing != nil,
	}
	if setupCtx.setup == nil {
		return plan, nil
	}

	if identity, identityErr := setupCtx.setup.FindDeployIdentity(ctx); identityErr != nil {
		plan.DeployIdentityError = identityErr.Error()
	} else if identity == nil {
		plan.DeployIdentityError = deployIdentityNotFound
	} else {
		plan.DeployIdentity = identity
	}

	if strings.TrimSpace(req.Target.Mode) == "" {
		return plan, nil
	}
	target, err := infisical.NormalizeSetupTarget(req.Target, project.Name)
	if err != nil {
		plan.RemoteError = err.Error()
		return plan, nil
	}
	if target.Mode == secretsourcetypes.SetupModeNewProject {
		if taken, takenErr := setupCtx.setup.ProjectNameTaken(ctx, target.ProjectName); takenErr == nil {
			plan.ProjectNameTaken = taken
		}
	}
	remote, err := setupCtx.setup.RemoteValues(ctx, target)
	if err != nil {
		plan.RemoteError = err.Error()
		return plan, nil
	}
	for i := range plan.Variables {
		plan.Variables[i].Remote = remoteStateInternal(plan.Variables[i], setupCtx.envValues, remote)
	}
	return plan, nil
}

func remoteStateInternal(variable secretsourcetypes.SetupVariable, local, remote map[string]string) string {
	remoteValue, inRemote := remote[variable.Key]
	localValue, inLocal := local[variable.Key]
	switch {
	case !inRemote:
		return secretsourcetypes.SetupRemoteMissing
	case !inLocal:
		return secretsourcetypes.SetupRemoteExists
	case remoteValue == localValue:
		return secretsourcetypes.SetupRemoteSame
	default:
		return secretsourcetypes.SetupRemoteDifferent
	}
}

// ApplySetup creates the Infisical side of a project and binds it, step by
// step. It stops at the first step that leaves nothing useful to continue
// with. The .env is only edited after the deploy identity reads every moved
// value back unchanged.
func (s *SecretSourceService) ApplySetup(ctx context.Context, project ProjectRef, req secretsourcetypes.SetupApplyRequest, actor usertypes.Actor) (secretsourcetypes.SetupResult, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()

	setupCtx, err := s.loadSetupContextInternal(ctx, project, req.SourceID)
	if err != nil {
		return secretsourcetypes.SetupResult{}, err
	}
	if setupCtx.setup == nil {
		return secretsourcetypes.SetupResult{}, common.Classify(common.ErrSecretSourceInvalid, infisical.ErrNoSetupIdentity)
	}
	target, keys, err := validateApplyInternal(req, project.Name)
	if err != nil {
		return secretsourcetypes.SetupResult{}, common.Classify(common.ErrValidation, err)
	}

	run := &setupRunInternal{}
	setup := setupCtx.setup

	// A second project with the same name is almost always a re-run after a
	// failed step; use the existing one instead.
	if target.Mode == secretsourcetypes.SetupModeNewProject {
		taken, takenErr := setup.ProjectNameTaken(ctx, target.ProjectName)
		if takenErr != nil {
			return secretsourcetypes.SetupResult{}, common.Classify(common.ErrUnavailable,
				fmt.Errorf("could not check for an existing project named %q: %w", target.ProjectName, takenErr))
		}
		if taken {
			return secretsourcetypes.SetupResult{}, common.Classify(common.ErrConflict,
				fmt.Errorf("an Infisical project named %q already exists; choose Existing project to use it", target.ProjectName))
		}
	}

	// 1. Project.
	projectID := target.ProjectID
	if target.Mode == secretsourcetypes.SetupModeNewProject {
		projectID, err = setup.CreateProject(ctx, target.ProjectName, target.Environment)
		if err != nil {
			return run.failInternal(setupStepProject, err), nil
		}
		run.addInternal(setupStepProject, secretsourcetypes.SetupStepDone, fmt.Sprintf("Created project %q (ID %s)", target.ProjectName, projectID))
		target.ProjectID = projectID
	} else {
		run.addInternal(setupStepProject, secretsourcetypes.SetupStepSkipped, "Using the existing project")
	}

	// 2. Folder.
	if target.SecretPath == "/" {
		run.addInternal(setupStepFolder, secretsourcetypes.SetupStepSkipped, "Using the root path")
	} else {
		created, folderErr := setup.EnsurePath(ctx, projectID, target.Environment, target.SecretPath)
		if folderErr != nil {
			return run.failInternal(setupStepFolder, folderErr), nil
		}
		if len(created) == 0 {
			run.addInternal(setupStepFolder, secretsourcetypes.SetupStepSkipped, target.SecretPath+" already exists")
		} else {
			run.addInternal(setupStepFolder, secretsourcetypes.SetupStepDone, "Created "+strings.Join(created, ", "))
		}
	}

	// 3. Secrets.
	remote, err := setup.RemoteValues(ctx, target)
	if err != nil {
		return run.failInternal(setupStepSecrets, err), nil
	}
	plan := planWritesInternal(keys, req, setupCtx.envValues, remote)
	pending, err := setup.WriteSecrets(ctx, projectID, target.Environment, target.SecretPath, plan.create, plan.overwrite)
	if err != nil {
		return run.failInternal(setupStepSecrets, err), nil
	}
	secretsDetail := fmt.Sprintf("Created %d, replaced %d, kept %d existing", len(plan.create), len(plan.overwrite), len(plan.kept))
	if pending {
		run.addInternal(setupStepSecrets, secretsourcetypes.SetupStepPending, secretsDetail+"; an Infisical approval policy holds the change until it is approved")
	} else {
		run.addInternal(setupStepSecrets, secretsourcetypes.SetupStepDone, secretsDetail)
	}

	// 4. Deploy identity access.
	if !req.GrantDeployIdentity {
		run.addInternal(setupStepGrant, secretsourcetypes.SetupStepSkipped, "Not requested")
	} else {
		run.addResultInternal(setupStepGrant, s.grantDeployIdentityInternal(ctx, setup, projectID))
	}

	// 5. Binding. It starts optional so a failed verification cannot block
	// deploys, and becomes required once the deploy identity reads it.
	bindingRequest := secretsourcetypes.UpsertBindingRequest{
		SourceID: setupCtx.source.ID,
		Target: secretsourcetypes.BindingTarget{Infisical: &secretsourcetypes.InfisicalTarget{
			ProjectID:        projectID,
			Environment:      target.Environment,
			SecretPath:       target.SecretPath,
			IncludeImports:   true,
			ExpandReferences: true,
		}},
		Required:     false,
		Enabled:      true,
		AutoRedeploy: req.AutoRedeploy,
	}
	binding, err := s.UpsertBinding(ctx, project.ID, bindingRequest)
	if err != nil {
		return run.failInternal(setupStepBinding, err), nil
	}
	run.result.Binding = binding
	bindingDetail := "Bound the project to " + infisical.Describe(binding.Target.Infisical)

	// 6. Verify with the deploy identity.
	verified, verifyStep := s.verifySetupInternal(ctx, setupCtx.source, binding.Target, keys, plan, setupCtx.envValues, pending)
	if req.Required {
		if verifyStep.Status == secretsourcetypes.SetupStepDone {
			bindingRequest.Required = true
			if binding, err = s.UpsertBinding(ctx, project.ID, bindingRequest); err != nil {
				run.addResultInternal(setupStepVerify, verifyStep)
				return run.failInternal(setupStepBinding, err), nil
			}
			run.result.Binding = binding
		} else {
			bindingDetail += "; left optional until the deploy identity can read the secrets"
		}
	}
	run.addInternal(setupStepBinding, secretsourcetypes.SetupStepDone, bindingDetail)
	run.addResultInternal(setupStepVerify, verifyStep)

	// 7. .env.
	run.addResultInternal(setupStepEnvFile, s.cleanEnvFileInternal(ctx, project, req, setupCtx, verified, verifyStep.Status, actor, &run.result))

	run.result.OK = !slices.ContainsFunc(run.result.Steps, func(step secretsourcetypes.SetupStep) bool {
		return step.Status == secretsourcetypes.SetupStepFailed || step.Status == secretsourcetypes.SetupStepPending
	})
	slog.InfoContext(ctx, "Project secret setup finished", "projectId", project.ID, "sourceId", setupCtx.source.ID, "ok", run.result.OK, "user", actor.Username)
	return run.result, nil
}

type setupRunInternal struct {
	result secretsourcetypes.SetupResult
}

func (r *setupRunInternal) addInternal(id, status, detail string) {
	r.result.Steps = append(r.result.Steps, secretsourcetypes.SetupStep{ID: id, Status: status, Detail: detail})
}

func (r *setupRunInternal) addResultInternal(id string, step secretsourcetypes.SetupStep) {
	step.ID = id
	r.result.Steps = append(r.result.Steps, step)
}

func (r *setupRunInternal) failInternal(id string, err error) secretsourcetypes.SetupResult {
	r.addInternal(id, secretsourcetypes.SetupStepFailed, err.Error())
	r.result.OK = false
	return r.result
}

// validateApplyInternal checks the request before anything is written.
func validateApplyInternal(req secretsourcetypes.SetupApplyRequest, projectName string) (secretsourcetypes.SetupTarget, []string, error) {
	target, err := infisical.NormalizeSetupTarget(req.Target, projectName)
	if err != nil {
		return target, nil, err
	}
	switch req.Values {
	case secretsourcetypes.SetupValuesImport, secretsourcetypes.SetupValuesPlaceholder:
	default:
		return target, nil, fmt.Errorf("values must be %s or %s", secretsourcetypes.SetupValuesImport, secretsourcetypes.SetupValuesPlaceholder)
	}
	switch req.EnvFile {
	case secretsourcetypes.SetupEnvFileKeep:
	case secretsourcetypes.SetupEnvFileRemove:
		if req.Values != secretsourcetypes.SetupValuesImport {
			return target, nil, errors.New("keys can only be removed from the .env when their values are imported")
		}
	default:
		return target, nil, fmt.Errorf("envFile must be %s or %s", secretsourcetypes.SetupEnvFileKeep, secretsourcetypes.SetupEnvFileRemove)
	}

	keys := make([]string, 0, len(req.Keys))
	for _, key := range req.Keys {
		key = strings.TrimSpace(key)
		if !envKeyPattern.MatchString(key) || strings.HasPrefix(key, "COMPOSE_") {
			return target, nil, fmt.Errorf("%q cannot be moved to a secret manager", key)
		}
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return target, nil, errors.New("choose at least one variable")
	}
	slices.Sort(keys)
	return target, keys, nil
}

type writePlanInternal struct {
	create    []infisicalclient.SecretInput
	overwrite []infisicalclient.SecretInput
	kept      []string
	// moved are keys whose Infisical value should now equal the .env value.
	moved []string
}

// planWritesInternal decides per key: create it when missing, replace it when
// the user chose to overwrite a different value, and otherwise keep
// Infisical's value. Placeholders never replace anything.
func planWritesInternal(keys []string, req secretsourcetypes.SetupApplyRequest, local, remote map[string]string) writePlanInternal {
	importing := req.Values == secretsourcetypes.SetupValuesImport
	plan := writePlanInternal{}
	for _, key := range keys {
		localValue, inLocal := local[key]
		remoteValue, inRemote := remote[key]
		value := ""
		if importing {
			value = localValue
		}
		switch {
		case !inRemote:
			plan.create = append(plan.create, infisicalclient.SecretInput{Key: key, Value: value, Comment: "Created by Arcane"})
			if importing && inLocal {
				plan.moved = append(plan.moved, key)
			}
		case importing && inLocal && remoteValue == localValue:
			plan.kept = append(plan.kept, key)
			plan.moved = append(plan.moved, key)
		case importing && inLocal && slices.Contains(req.OverwriteKeys, key):
			plan.overwrite = append(plan.overwrite, infisicalclient.SecretInput{Key: key, Value: value})
			plan.moved = append(plan.moved, key)
		default:
			plan.kept = append(plan.kept, key)
		}
	}
	return plan
}

func (s *SecretSourceService) grantDeployIdentityInternal(ctx context.Context, setup *infisical.Setup, projectID string) secretsourcetypes.SetupStep {
	identity, err := setup.FindDeployIdentity(ctx)
	if err != nil {
		return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepFailed, Detail: err.Error()}
	}
	if identity == nil {
		return secretsourcetypes.SetupStep{
			Status: secretsourcetypes.SetupStepFailed,
			Detail: "could not find the deploy identity; add it to the Infisical project with the viewer role",
		}
	}
	already, err := setup.GrantDeployIdentity(ctx, projectID, identity.ID)
	if err != nil {
		return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepFailed, Detail: err.Error()}
	}
	if already {
		return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepSkipped, Detail: identity.Name + " already has access"}
	}
	return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepDone, Detail: "Gave " + identity.Name + " the " + infisical.DeployRole + " role"}
}

// verifySetupInternal fetches the new binding with the deploy identity, the
// way a deploy would. It returns the moved keys whose value came back exactly
// as in the .env; only those may leave the .env.
func (s *SecretSourceService) verifySetupInternal(
	ctx context.Context,
	source *SecretSource,
	target secretsourcetypes.BindingTarget,
	keys []string,
	plan writePlanInternal,
	local map[string]string,
	pending bool,
) ([]string, secretsourcetypes.SetupStep) {
	if pending {
		return nil, secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepSkipped, Detail: "Waiting for the Infisical approval"}
	}
	provider, err := s.providerForSourceInternal(source)
	if err != nil {
		return nil, secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepFailed, Detail: err.Error()}
	}
	fetched, _, err := provider.Fetch(ctx, target)
	if err != nil {
		return nil, secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepFailed, Detail: "the deploy identity could not read the secrets: " + err.Error()}
	}

	missing := []string{}
	for _, key := range keys {
		if _, ok := fetched[key]; !ok {
			missing = append(missing, key)
		}
	}
	verified := []string{}
	changed := []string{}
	for _, key := range plan.moved {
		if value, ok := fetched[key]; ok && value == local[key] {
			verified = append(verified, key)
		} else if ok {
			changed = append(changed, key)
		}
	}

	switch {
	case len(missing) > 0:
		return verified, secretsourcetypes.SetupStep{
			Status: secretsourcetypes.SetupStepFailed,
			Detail: "the deploy identity cannot read " + strings.Join(missing, ", "),
		}
	case len(changed) > 0:
		// Reference expansion or an import can change what a deploy receives.
		return verified, secretsourcetypes.SetupStep{
			Status: secretsourcetypes.SetupStepDone,
			Detail: fmt.Sprintf("The deploy identity reads all %d keys; %s resolve to different values (references or imports), so they stay in the .env", len(keys), strings.Join(changed, ", ")),
		}
	default:
		return verified, secretsourcetypes.SetupStep{
			Status: secretsourcetypes.SetupStepDone,
			Detail: fmt.Sprintf("The deploy identity reads all %d keys", len(keys)),
		}
	}
}

func (s *SecretSourceService) cleanEnvFileInternal(
	ctx context.Context,
	project ProjectRef,
	req secretsourcetypes.SetupApplyRequest,
	setupCtx *setupContextInternal,
	verified []string,
	verifyStatus string,
	actor usertypes.Actor,
	result *secretsourcetypes.SetupResult,
) secretsourcetypes.SetupStep {
	if req.EnvFile != secretsourcetypes.SetupEnvFileRemove {
		return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepSkipped, Detail: "The .env was left as is; Infisical values take precedence at deploy"}
	}
	if verifyStatus != secretsourcetypes.SetupStepDone {
		return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepSkipped, Detail: "Nothing was removed because verification did not pass"}
	}

	expected := make(map[string]string, len(verified))
	fromGit := []string{}
	for _, variable := range setupCtx.variables {
		if !variable.InEnvFile || !slices.Contains(verified, variable.Key) {
			continue
		}
		if variable.FromGit {
			fromGit = append(fromGit, variable.Key)
			continue
		}
		expected[variable.Key] = setupCtx.envValues[variable.Key]
	}
	notes := []string{}
	if len(fromGit) > 0 {
		notes = append(notes, strings.Join(fromGit, ", ")+" come from the Git repository's env file; remove them there")
	}
	if len(expected) == 0 {
		return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepSkipped, Detail: joinNotesInternal("No keys to remove", notes)}
	}

	removal, err := s.projectFiles.RemoveSecretEnvKeys(ctx, project.ID, expected, req.KeepBackup, actor)
	if err != nil {
		return secretsourcetypes.SetupStep{
			Status: secretsourcetypes.SetupStepFailed,
			Detail: "the secrets are in Infisical, but the .env could not be updated: " + err.Error(),
		}
	}
	result.RemovedKeys = removal.Removed
	result.BackupFile = removal.BackupFile
	if len(removal.Referenced) > 0 {
		notes = append(notes, strings.Join(removal.Referenced, ", ")+" stay because other .env entries use them")
	}
	if len(removal.Changed) > 0 {
		notes = append(notes, strings.Join(removal.Changed, ", ")+" stay because they changed in the .env during setup")
	}
	detail := fmt.Sprintf("Removed %d keys from the .env", len(removal.Removed))
	if removal.BackupFile != "" {
		detail += "; the previous file is saved as " + removal.BackupFile + ". Delete it once deploys work"
	}
	return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepDone, Detail: joinNotesInternal(detail, notes)}
}

func joinNotesInternal(detail string, notes []string) string {
	if len(notes) == 0 {
		return detail
	}
	return detail + ". " + strings.Join(notes, ". ")
}
