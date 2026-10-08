package secretsource

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/internal/secretsource/children/bitwarden"
	"github.com/getarcaneapp/arcane/backend/v2/internal/secretsource/children/infisical"
	infisicalclient "github.com/getarcaneapp/arcane/backend/v2/pkg/infisical"
)

// setupWriterInternal is one provider's side of project setup. A writer is
// created per plan or apply and may keep state between its calls.
type setupWriterInternal interface {
	NormalizeTarget(target secretsourcetypes.SetupTarget, arcaneProjectName string) (secretsourcetypes.SetupTarget, error)
	// CreatesContainer reports whether the target creates a project or folder.
	CreatesContainer(target secretsourcetypes.SetupTarget) bool
	// NameTaken reports whether the project or folder a target would create
	// already exists.
	NameTaken(ctx context.Context, target secretsourcetypes.SetupTarget) (bool, error)
	// Prepare creates what the target needs and returns it with IDs filled in,
	// plus a step per thing it did. On error the steps are those completed.
	Prepare(ctx context.Context, target secretsourcetypes.SetupTarget) (secretsourcetypes.SetupTarget, []secretsourcetypes.SetupStep, error)
	// RemoteValues returns the values already at the target, by key.
	RemoteValues(ctx context.Context, target secretsourcetypes.SetupTarget) (map[string]string, error)
	// Write creates the create keys and replaces the overwrite keys with their
	// values. pending reports a change held for approval.
	Write(ctx context.Context, target secretsourcetypes.SetupTarget, values map[string]string, create, overwrite []string) (pending bool, err error)
	// DeployIdentity finds the identity deploys read with, or nil when the
	// provider has no separate one.
	DeployIdentity(ctx context.Context) (*secretsourcetypes.SetupIdentity, error)
	// Grant gives the deploy identity read access to the target.
	Grant(ctx context.Context, target secretsourcetypes.SetupTarget) secretsourcetypes.SetupStep
	BindingTarget(target secretsourcetypes.SetupTarget) secretsourcetypes.BindingTarget
}

// newSetupWriterInternal returns the source's writer, or nil when the source
// cannot write (an Infisical source without a setup identity).
func newSetupWriterInternal(httpClient *http.Client, source *SecretSource) (setupWriterInternal, error) {
	switch source.Provider {
	case secretsourcetypes.ProviderInfisical:
		if source.Settings.Infisical == nil {
			return nil, common.Classify(common.ErrSecretSourceInvalid, errors.New("infisical settings are required"))
		}
		setupSecret, err := decryptCredentialInternal(source.SetupCredential)
		if err != nil {
			return nil, err
		}
		setup, err := infisical.NewSetup(httpClient, *source.Settings.Infisical, setupSecret)
		if errors.Is(err, infisical.ErrNoSetupIdentity) {
			return nil, nil
		}
		if err != nil {
			return nil, common.Classify(common.ErrSecretSourceInvalid, err)
		}
		return &infisicalWriterInternal{setup: setup}, nil
	case secretsourcetypes.ProviderBitwarden:
		if source.Settings.Bitwarden == nil {
			return nil, common.Classify(common.ErrSecretSourceInvalid, errors.New("bitwarden settings are required"))
		}
		setup, err := bitwarden.NewSetup(httpClient, *source.Settings.Bitwarden)
		if err != nil {
			return nil, common.Classify(common.ErrSecretSourceInvalid, err)
		}
		return &bitwardenWriterInternal{setup: setup}, nil
	default:
		return nil, common.Classify(common.ErrSecretSourceInvalid, fmt.Errorf("unknown provider %q", source.Provider))
	}
}

// ---- Infisical ----

type infisicalWriterInternal struct {
	setup *infisical.Setup
}

func (w *infisicalWriterInternal) NormalizeTarget(target secretsourcetypes.SetupTarget, arcaneProjectName string) (secretsourcetypes.SetupTarget, error) {
	return infisical.NormalizeSetupTarget(target, arcaneProjectName)
}

func (w *infisicalWriterInternal) CreatesContainer(target secretsourcetypes.SetupTarget) bool {
	return target.Mode == secretsourcetypes.SetupModeNewProject
}

func (w *infisicalWriterInternal) NameTaken(ctx context.Context, target secretsourcetypes.SetupTarget) (bool, error) {
	return w.setup.ProjectNameTaken(ctx, target.ProjectName)
}

func (w *infisicalWriterInternal) Prepare(ctx context.Context, target secretsourcetypes.SetupTarget) (secretsourcetypes.SetupTarget, []secretsourcetypes.SetupStep, error) {
	steps := []secretsourcetypes.SetupStep{}
	if target.Mode == secretsourcetypes.SetupModeNewProject {
		projectID, err := w.setup.CreateProject(ctx, target.ProjectName, target.Environment)
		if err != nil {
			return target, steps, stepErrorInternal{step: setupStepProject, err: err}
		}
		target.ProjectID = projectID
		steps = append(steps, doneStepInternal(setupStepProject, fmt.Sprintf("Created project %q (ID %s)", target.ProjectName, projectID)))
	} else {
		steps = append(steps, skippedStepInternal(setupStepProject, "Using the existing project"))
	}

	if target.SecretPath == "/" {
		return target, append(steps, skippedStepInternal(setupStepFolder, "Using the root path")), nil
	}
	created, err := w.setup.EnsurePath(ctx, target.ProjectID, target.Environment, target.SecretPath)
	if err != nil {
		return target, steps, stepErrorInternal{step: setupStepFolder, err: err}
	}
	if len(created) == 0 {
		return target, append(steps, skippedStepInternal(setupStepFolder, target.SecretPath+" already exists")), nil
	}
	return target, append(steps, doneStepInternal(setupStepFolder, "Created "+strings.Join(created, ", "))), nil
}

func (w *infisicalWriterInternal) RemoteValues(ctx context.Context, target secretsourcetypes.SetupTarget) (map[string]string, error) {
	return w.setup.RemoteValues(ctx, target)
}

func (w *infisicalWriterInternal) Write(ctx context.Context, target secretsourcetypes.SetupTarget, values map[string]string, create, overwrite []string) (bool, error) {
	toInput := func(keys []string, comment string) []infisicalclient.SecretInput {
		inputs := make([]infisicalclient.SecretInput, 0, len(keys))
		for _, key := range keys {
			inputs = append(inputs, infisicalclient.SecretInput{Key: key, Value: values[key], Comment: comment})
		}
		return inputs
	}
	return w.setup.WriteSecrets(ctx, target.ProjectID, target.Environment, target.SecretPath, toInput(create, "Created by Arcane"), toInput(overwrite, ""))
}

func (w *infisicalWriterInternal) DeployIdentity(ctx context.Context) (*secretsourcetypes.SetupIdentity, error) {
	return w.setup.FindDeployIdentity(ctx)
}

func (w *infisicalWriterInternal) Grant(ctx context.Context, target secretsourcetypes.SetupTarget) secretsourcetypes.SetupStep {
	identity, err := w.setup.FindDeployIdentity(ctx)
	if err != nil {
		return failedStepInternal(setupStepGrant, err.Error())
	}
	if identity == nil {
		return failedStepInternal(setupStepGrant, "could not find the deploy identity; add it to the Infisical project with the viewer role")
	}
	already, err := w.setup.GrantDeployIdentity(ctx, target.ProjectID, identity.ID)
	if err != nil {
		return failedStepInternal(setupStepGrant, err.Error())
	}
	if already {
		return skippedStepInternal(setupStepGrant, identity.Name+" already has access")
	}
	return doneStepInternal(setupStepGrant, "Gave "+identity.Name+" the "+infisical.DeployRole+" role")
}

func (w *infisicalWriterInternal) BindingTarget(target secretsourcetypes.SetupTarget) secretsourcetypes.BindingTarget {
	return secretsourcetypes.BindingTarget{Infisical: &secretsourcetypes.InfisicalTarget{
		ProjectID:        target.ProjectID,
		Environment:      target.Environment,
		SecretPath:       target.SecretPath,
		IncludeImports:   true,
		ExpandReferences: true,
	}}
}

// ---- Bitwarden ----

type bitwardenWriterInternal struct {
	setup *bitwarden.Setup
	// items are the target folder's items from the last RemoteValues call;
	// updates need their IDs.
	items map[string]bitwarden.RemoteItem
}

func (w *bitwardenWriterInternal) NormalizeTarget(target secretsourcetypes.SetupTarget, arcaneProjectName string) (secretsourcetypes.SetupTarget, error) {
	return bitwarden.NormalizeSetupTarget(target, arcaneProjectName)
}

func (w *bitwardenWriterInternal) CreatesContainer(target secretsourcetypes.SetupTarget) bool {
	return target.Mode == secretsourcetypes.SetupModeNewFolder
}

func (w *bitwardenWriterInternal) NameTaken(ctx context.Context, target secretsourcetypes.SetupTarget) (bool, error) {
	return w.setup.FolderNameTaken(ctx, target.FolderName)
}

func (w *bitwardenWriterInternal) Prepare(ctx context.Context, target secretsourcetypes.SetupTarget) (secretsourcetypes.SetupTarget, []secretsourcetypes.SetupStep, error) {
	if target.Mode != secretsourcetypes.SetupModeNewFolder {
		return target, []secretsourcetypes.SetupStep{skippedStepInternal(setupStepFolder, "Using the existing folder")}, nil
	}
	folderID, err := w.setup.CreateFolder(ctx, target.FolderName)
	if err != nil {
		return target, nil, stepErrorInternal{step: setupStepFolder, err: err}
	}
	target.FolderID = folderID
	return target, []secretsourcetypes.SetupStep{doneStepInternal(setupStepFolder, fmt.Sprintf("Created folder %q", target.FolderName))}, nil
}

func (w *bitwardenWriterInternal) RemoteValues(ctx context.Context, target secretsourcetypes.SetupTarget) (map[string]string, error) {
	items, err := w.setup.RemoteItems(ctx, target.FolderID)
	if err != nil {
		return nil, err
	}
	w.items = items
	values := make(map[string]string, len(items))
	for name, item := range items {
		values[name] = item.Value
	}
	return values, nil
}

func (w *bitwardenWriterInternal) Write(ctx context.Context, target secretsourcetypes.SetupTarget, values map[string]string, create, overwrite []string) (bool, error) {
	if err := w.setup.CreateItems(ctx, target.FolderID, values, create); err != nil {
		return false, err
	}
	itemIDs := make(map[string]string, len(overwrite))
	for _, key := range overwrite {
		item, ok := w.items[key]
		if !ok {
			return false, fmt.Errorf("update %s: the item is no longer in the folder", key)
		}
		itemIDs[key] = item.ID
	}
	return false, w.setup.UpdateItems(ctx, values, itemIDs, overwrite)
}

func (w *bitwardenWriterInternal) DeployIdentity(context.Context) (*secretsourcetypes.SetupIdentity, error) {
	return nil, nil
}

func (w *bitwardenWriterInternal) Grant(context.Context, secretsourcetypes.SetupTarget) secretsourcetypes.SetupStep {
	return skippedStepInternal(setupStepGrant, "Not needed: bw serve reads with the account that owns the folder")
}

func (w *bitwardenWriterInternal) BindingTarget(target secretsourcetypes.SetupTarget) secretsourcetypes.BindingTarget {
	return secretsourcetypes.BindingTarget{Bitwarden: &secretsourcetypes.BitwardenTarget{
		Scope: secretsourcetypes.BitwardenScopeFolder,
		ID:    target.FolderID,
		Name:  target.FolderName,
	}}
}

// ---- Steps ----

// stepErrorInternal is a failure of a named step.
type stepErrorInternal struct {
	step string
	err  error
}

func (e stepErrorInternal) Error() string { return e.err.Error() }
func (e stepErrorInternal) Unwrap() error { return e.err }

func doneStepInternal(id, detail string) secretsourcetypes.SetupStep {
	return secretsourcetypes.SetupStep{ID: id, Status: secretsourcetypes.SetupStepDone, Detail: detail}
}

func skippedStepInternal(id, detail string) secretsourcetypes.SetupStep {
	return secretsourcetypes.SetupStep{ID: id, Status: secretsourcetypes.SetupStepSkipped, Detail: detail}
}

func failedStepInternal(id, detail string) secretsourcetypes.SetupStep {
	return secretsourcetypes.SetupStep{ID: id, Status: secretsourcetypes.SetupStepFailed, Detail: detail}
}
