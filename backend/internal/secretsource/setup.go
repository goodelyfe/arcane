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
	"github.com/getarcaneapp/arcane/backend/v2/internal/secretsource/children/vault"
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
	source *SecretSource
	// writer is nil when the source cannot write.
	writer    setupWriterInternal
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
	writer, err := newSetupWriterInternal(s.httpClient, source)
	if err != nil {
		return nil, err
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
	return &setupContextInternal{source: source, writer: writer, variables: variables, envValues: envValues, files: files}, nil
}

// PlanSetup lists the project's variables and, when a target is given, where
// each stands in the secret manager. It writes nothing and returns no values.
func (s *SecretSourceService) PlanSetup(ctx context.Context, project ProjectRef, req secretsourcetypes.SetupPlanRequest) (secretsourcetypes.SetupPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()

	setupCtx, err := s.loadSetupContextInternal(ctx, project, req.SourceID)
	if err != nil {
		return secretsourcetypes.SetupPlan{}, err
	}
	existing, err := s.loadBindingsInternal(ctx, project.ID)
	if err != nil {
		return secretsourcetypes.SetupPlan{}, err
	}

	plan := secretsourcetypes.SetupPlan{
		Provider:             setupCtx.source.Provider,
		SuggestedProjectName: project.Name,
		SuggestedSecretPath:  suggestedPathInternal(setupCtx.source.Provider, project.Name),
		SuggestedFolderName:  project.Name,
		CanWrite:             setupCtx.writer != nil,
		Variables:            setupCtx.variables,
		HasGitSource:         setupCtx.files.HasGitSource,
		AlreadyBound:         len(existing) > 0,
	}
	writer := setupCtx.writer
	if writer == nil {
		return plan, nil
	}

	if setupCtx.source.Provider == secretsourcetypes.ProviderInfisical {
		if identity, identityErr := writer.DeployIdentity(ctx); identityErr != nil {
			plan.DeployIdentityError = identityErr.Error()
		} else if identity == nil {
			plan.DeployIdentityError = deployIdentityNotFound
		} else {
			plan.DeployIdentity = identity
		}
	}

	if strings.TrimSpace(req.Target.Mode) == "" {
		return plan, nil
	}
	target, err := writer.NormalizeTarget(req.Target, project.Name)
	if err != nil {
		plan.RemoteError = err.Error()
		return plan, nil
	}
	if writer.CreatesContainer(target) {
		if taken, takenErr := writer.NameTaken(ctx, target); takenErr == nil {
			plan.ProjectNameTaken = taken
		}
		// Nothing exists there yet.
		for i := range plan.Variables {
			plan.Variables[i].Remote = secretsourcetypes.SetupRemoteMissing
		}
		return plan, nil
	}
	remote, err := writer.RemoteValues(ctx, target)
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

// ApplySetup creates the secret manager's side of a project and binds it,
// step by step. It stops at the first step that leaves nothing useful to
// continue with. The .env is only edited after a deploy-style read returns
// every moved value unchanged.
func (s *SecretSourceService) ApplySetup(ctx context.Context, project ProjectRef, req secretsourcetypes.SetupApplyRequest, actor usertypes.Actor) (secretsourcetypes.SetupResult, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()

	setupCtx, err := s.loadSetupContextInternal(ctx, project, req.SourceID)
	if err != nil {
		return secretsourcetypes.SetupResult{}, err
	}
	writer := setupCtx.writer
	if writer == nil {
		return secretsourcetypes.SetupResult{}, common.Classify(common.ErrSecretSourceInvalid, infisical.ErrNoSetupIdentity)
	}
	target, keys, err := validateApplyInternal(writer, req, project.Name)
	if err != nil {
		return secretsourcetypes.SetupResult{}, common.Classify(common.ErrValidation, err)
	}

	// A second project or folder with the same name is almost always a re-run
	// after a failed step; use the existing one instead.
	if writer.CreatesContainer(target) {
		taken, takenErr := writer.NameTaken(ctx, target)
		if takenErr != nil {
			return secretsourcetypes.SetupResult{}, common.Classify(common.ErrUnavailable, fmt.Errorf("could not check for an existing project or folder: %w", takenErr))
		}
		if taken {
			return secretsourcetypes.SetupResult{}, common.Classify(common.ErrConflict,
				errors.New("a project or folder with this name already exists; choose the existing one instead"))
		}
	}

	run := &setupRunInternal{}

	// 1. Project and folder.
	target, steps, err := writer.Prepare(ctx, target)
	run.result.Steps = append(run.result.Steps, steps...)
	if err != nil {
		step := setupStepProject
		if stepErr, ok := errors.AsType[stepErrorInternal](err); ok {
			step = stepErr.step
		}
		return run.failInternal(step, err), nil
	}

	// 2. Secrets.
	remote := map[string]string{}
	if !writer.CreatesContainer(target) {
		if remote, err = writer.RemoteValues(ctx, target); err != nil {
			return run.failInternal(setupStepSecrets, err), nil
		}
	}
	plan := planWritesInternal(keys, req, setupCtx.envValues, remote)
	pending, err := writer.Write(ctx, target, plan.values, plan.create, plan.overwrite)
	if err != nil {
		return run.failInternal(setupStepSecrets, err), nil
	}
	secretsDetail := fmt.Sprintf("Created %d, replaced %d, kept %d existing", len(plan.create), len(plan.overwrite), len(plan.kept))
	if pending {
		run.addInternal(setupStepSecrets, secretsourcetypes.SetupStepPending, secretsDetail+"; an approval policy holds the change until it is approved")
	} else {
		run.addInternal(setupStepSecrets, secretsourcetypes.SetupStepDone, secretsDetail)
	}

	// 3. Read access for deploys.
	if req.GrantDeployIdentity {
		run.addResultInternal(setupStepGrant, writer.Grant(ctx, target))
	} else {
		run.addInternal(setupStepGrant, secretsourcetypes.SetupStepSkipped, "Not requested")
	}

	// 4. Binding. It starts optional so a failed verification cannot block
	// deploys, and becomes required once a deploy-style read works.
	bindingRequest := secretsourcetypes.UpsertBindingRequest{
		SourceID:     setupCtx.source.ID,
		Target:       writer.BindingTarget(target),
		Required:     false,
		Enabled:      true,
		AutoRedeploy: req.AutoRedeploy,
	}
	binding, err := s.bindTargetInternal(ctx, project.ID, bindingRequest)
	if err != nil {
		return run.failInternal(setupStepBinding, err), nil
	}
	run.result.Binding = binding
	bindingDetail := "Bound the project to " + describeTargetInternal(&ProjectSecretBinding{Target: binding.Target})

	// 5. Verify the way a deploy reads.
	verified, verifyStep := s.verifySetupInternal(ctx, setupCtx.source, binding.Target, keys, plan, setupCtx.envValues, pending)
	if req.Required {
		if verifyStep.Status == secretsourcetypes.SetupStepDone {
			bindingRequest.Required = true
			if binding, err = s.UpdateBinding(ctx, project.ID, binding.ID, bindingRequest); err != nil {
				run.addResultInternal(setupStepVerify, verifyStep)
				return run.failInternal(setupStepBinding, err), nil
			}
			run.result.Binding = binding
		} else {
			bindingDetail += "; left optional until deploys can read the secrets"
		}
	}
	run.addInternal(setupStepBinding, secretsourcetypes.SetupStepDone, bindingDetail)
	run.addResultInternal(setupStepVerify, verifyStep)

	// 6. .env.
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
func validateApplyInternal(writer setupWriterInternal, req secretsourcetypes.SetupApplyRequest, projectName string) (secretsourcetypes.SetupTarget, []string, error) {
	target, err := writer.NormalizeTarget(req.Target, projectName)
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
	// values are what setup writes per key: the .env value, or empty for
	// placeholders.
	values    map[string]string
	create    []string
	overwrite []string
	kept      []string
	// moved are keys whose secret manager value should now equal the .env
	// value.
	moved []string
}

// planWritesInternal decides per key: create it when missing, replace it when
// the user chose to overwrite a different value, and otherwise keep the
// existing value. Placeholders never replace anything.
func planWritesInternal(keys []string, req secretsourcetypes.SetupApplyRequest, local, remote map[string]string) writePlanInternal {
	importing := req.Values == secretsourcetypes.SetupValuesImport
	plan := writePlanInternal{values: make(map[string]string, len(keys))}
	for _, key := range keys {
		localValue, inLocal := local[key]
		remoteValue, inRemote := remote[key]
		if importing {
			plan.values[key] = localValue
		} else {
			plan.values[key] = ""
		}
		switch {
		case !inRemote:
			plan.create = append(plan.create, key)
			if importing && inLocal {
				plan.moved = append(plan.moved, key)
			}
		case importing && inLocal && remoteValue == localValue:
			plan.kept = append(plan.kept, key)
			plan.moved = append(plan.moved, key)
		case importing && inLocal && slices.Contains(req.OverwriteKeys, key):
			plan.overwrite = append(plan.overwrite, key)
			plan.moved = append(plan.moved, key)
		default:
			plan.kept = append(plan.kept, key)
		}
	}
	return plan
}

// verifySetupInternal fetches the new binding the way a deploy would. It returns the moved keys whose value came back exactly
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
		return nil, secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepSkipped, Detail: "Waiting for the approval"}
	}
	provider, err := s.providerForSourceInternal(source)
	if err != nil {
		return nil, secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepFailed, Detail: err.Error()}
	}
	fetched, skipped, err := provider.Fetch(ctx, target)
	if err != nil {
		return nil, secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepFailed, Detail: "deploys could not read the secrets: " + err.Error()}
	}

	// Some providers skip entries without a value (an empty Bitwarden note);
	// a key written empty and read back as skipped is still in place.
	readBack := func(key string) (string, bool) {
		if value, ok := fetched[key]; ok {
			return value, true
		}
		if plan.values[key] == "" && slices.Contains(skipped, key) {
			return "", true
		}
		return "", false
	}

	missing := []string{}
	for _, key := range keys {
		if _, ok := readBack(key); !ok {
			missing = append(missing, key)
		}
	}
	verified := []string{}
	changed := []string{}
	for _, key := range plan.moved {
		if value, ok := readBack(key); ok && value == local[key] {
			verified = append(verified, key)
		} else if ok {
			changed = append(changed, key)
		}
	}

	switch {
	case len(missing) > 0:
		return verified, secretsourcetypes.SetupStep{
			Status: secretsourcetypes.SetupStepFailed,
			Detail: "deploys cannot read " + strings.Join(missing, ", "),
		}
	case len(changed) > 0:
		// Reference expansion or an import can change what a deploy receives.
		return verified, secretsourcetypes.SetupStep{
			Status: secretsourcetypes.SetupStepDone,
			Detail: fmt.Sprintf("Deploys read all %d keys; %s resolve to different values (references or imports), so they stay in the .env",
				len(keys), strings.Join(changed, ", ")),
		}
	default:
		return verified, secretsourcetypes.SetupStep{
			Status: secretsourcetypes.SetupStepDone,
			Detail: fmt.Sprintf("Deploys read all %d keys", len(keys)),
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
		return secretsourcetypes.SetupStep{Status: secretsourcetypes.SetupStepSkipped, Detail: "The .env was left as is; secret manager values take precedence at deploy"}
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
			Detail: "the secrets are stored, but the .env could not be updated: " + err.Error(),
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

// suggestedPathInternal proposes where a project's secrets go: a folder for
// Infisical, a KV path for Vault/OpenBao.
func suggestedPathInternal(providerName, projectName string) string {
	if providerName == secretsourcetypes.ProviderVault {
		return vault.SuggestPath(projectName)
	}
	return infisical.SuggestFolder(projectName)
}
