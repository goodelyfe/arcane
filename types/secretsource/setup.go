package secretsource

// Project setup: moves a project's variables into Infisical and binds the
// project in one step. It writes with the source's setup identity; the
// binding it creates reads with the source's deploy identity as usual.

// Where setup puts the project's secrets.
const (
	// SetupModeNewProject creates an Infisical project named after the Arcane
	// project and uses the root path of one of its environments.
	SetupModeNewProject = "new-project"
	// SetupModeExistingProject uses a path of a project that already exists.
	SetupModeExistingProject = "existing-project"
	// SetupModeSharedFolder uses a folder per Arcane project inside a shared
	// project, such as /immich in a "homelab" project.
	SetupModeSharedFolder = "shared-folder"
)

// How setup fills the secrets it creates.
const (
	// SetupValuesImport copies the values from the project's .env.
	SetupValuesImport = "import"
	// SetupValuesPlaceholder creates the keys with empty values to fill in
	// Infisical.
	SetupValuesPlaceholder = "placeholder"
)

// What setup does with the imported keys in the project's .env.
const (
	SetupEnvFileKeep   = "keep"
	SetupEnvFileRemove = "remove"
)

// Where a variable stands in Infisical before setup.
const (
	SetupRemoteMissing   = "missing"
	SetupRemoteSame      = "same"
	SetupRemoteDifferent = "different"
	// SetupRemoteExists means Infisical has the key and the .env does not.
	SetupRemoteExists = "exists"
	// SetupRemoteUnknown means the target could not be read.
	SetupRemoteUnknown = "unknown"
)

// Outcome of one setup step.
const (
	SetupStepDone    = "done"
	SetupStepSkipped = "skipped"
	SetupStepFailed  = "failed"
	// SetupStepPending means Infisical holds the change for approval.
	SetupStepPending = "pending"
)

// SetupTarget is where setup writes in Infisical.
type SetupTarget struct {
	Mode string `json:"mode"`
	// ProjectName names the project to create (new-project).
	ProjectName string `json:"projectName,omitempty"`
	// ProjectID selects an existing project (existing-project, shared-folder).
	ProjectID string `json:"projectId,omitempty"`
	// Environment is the environment slug, such as prod.
	Environment string `json:"environment"`
	// SecretPath is the folder path. It is created when missing.
	SecretPath string `json:"secretPath,omitempty"`
}

type SetupPlanRequest struct {
	SourceID string      `json:"sourceId"`
	Target   SetupTarget `json:"target"`
}

// SetupVariable describes one variable the project uses. It never carries a
// value.
type SetupVariable struct {
	Key string `json:"key"`
	// InCompose is true when the compose files reference ${KEY}.
	InCompose bool `json:"inCompose"`
	// ComposeDefault is true when every reference has a default, such as
	// ${KEY:-value}, so compose works without it.
	ComposeDefault bool `json:"composeDefault"`
	// ComposeRequired is true when a reference fails without it, such as
	// ${KEY:?message}.
	ComposeRequired bool `json:"composeRequired"`
	// InEnvFile is true when the project's .env defines the key.
	InEnvFile bool `json:"inEnvFile"`
	// EnvEmpty is true when the .env defines the key with an empty value.
	EnvEmpty bool `json:"envEmpty"`
	// FromGit is true when the value comes from the Git repository's env
	// file, so it cannot be removed from the .env here.
	FromGit bool `json:"fromGit"`
	// SecretLike is true for names such as *_PASSWORD, *_TOKEN, or *_KEY.
	SecretLike bool `json:"secretLike"`
	// Remote is the key's state at the target: missing, same, different,
	// exists, or unknown.
	Remote string `json:"remote"`
}

// SetupIdentity is a machine identity found in Infisical.
type SetupIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SetupPlan is what setup would do, computed without writing anything.
type SetupPlan struct {
	// SuggestedProjectName and SuggestedSecretPath prefill the wizard.
	SuggestedProjectName string `json:"suggestedProjectName"`
	SuggestedSecretPath  string `json:"suggestedSecretPath"`
	// CanWrite is false when the source has no setup identity.
	CanWrite  bool            `json:"canWrite"`
	Variables []SetupVariable `json:"variables"`
	// ProjectNameTaken is true when a project with the requested name already
	// exists (new-project), so the wizard can offer to use it instead.
	ProjectNameTaken bool `json:"projectNameTaken"`
	// RemoteError explains why the target could not be read.
	RemoteError string `json:"remoteError,omitempty"`
	// DeployIdentity is the source's deploy identity, found by client ID, so
	// setup can give it read access to the new secrets.
	DeployIdentity      *SetupIdentity `json:"deployIdentity,omitempty"`
	DeployIdentityError string         `json:"deployIdentityError,omitempty"`
	// HasGitSource is true for projects synced from Git.
	HasGitSource bool `json:"hasGitSource"`
	// AlreadyBound is true when the project already has a binding, which
	// setup replaces.
	AlreadyBound bool `json:"alreadyBound"`
}

type SetupApplyRequest struct {
	SourceID string      `json:"sourceId"`
	Target   SetupTarget `json:"target"`
	// Keys are the variables to create in Infisical.
	Keys []string `json:"keys"`
	// Values is import or placeholder.
	Values string `json:"values"`
	// OverwriteKeys replace values that already differ in Infisical. Other
	// existing keys keep their Infisical value.
	OverwriteKeys []string `json:"overwriteKeys,omitempty"`
	// EnvFile is keep or remove. Removal happens only after the deploy
	// identity reads back every imported value unchanged.
	EnvFile string `json:"envFile"`
	// KeepBackup writes the previous .env next to it before removal.
	KeepBackup bool `json:"keepBackup"`
	// GrantDeployIdentity gives the deploy identity the viewer role on the
	// project.
	GrantDeployIdentity bool `json:"grantDeployIdentity"`
	Required            bool `json:"required"`
	AutoRedeploy        bool `json:"autoRedeploy"`
}

// SetupStep is one step of an applied setup, in order.
type SetupStep struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type SetupResult struct {
	// OK is true when every step is done or skipped.
	OK      bool        `json:"ok"`
	Steps   []SetupStep `json:"steps"`
	Binding *Binding    `json:"binding,omitempty"`
	// RemovedKeys were removed from the .env.
	RemovedKeys []string `json:"removedKeys,omitempty"`
	// BackupFile is the .env backup, relative to the project directory.
	BackupFile string `json:"backupFile,omitempty"`
}
