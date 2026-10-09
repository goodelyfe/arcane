export type SecretProvider = 'infisical' | 'bitwarden' | 'vault' | 'doppler' | 'onepassword' | 'http';

export type BitwardenScope = 'folder' | 'collection' | 'item';
export type OnePasswordScope = 'vault' | 'item';
export type VaultAuthMethod = 'token' | 'approle';

export interface InfisicalSettings {
	siteUrl: string;
	clientId: string;
	organizationSlug?: string;
	// Optional second identity used only by project setup to write to Infisical.
	setupClientId?: string;
}

export interface BitwardenSettings {
	serveUrl: string;
}

export interface VaultSettings {
	address: string;
	namespace?: string;
	authMethod: VaultAuthMethod;
	roleId?: string;
	appRoleMount?: string;
	// A separate token for guided setup is stored as the setup credential.
	setupToken?: boolean;
}

export interface DopplerSettings {
	apiUrl?: string;
}

export interface OnePasswordSettings {
	serverUrl: string;
}

export interface HttpSettings {
	baseUrl: string;
}

export interface SourceSettings {
	infisical?: InfisicalSettings;
	bitwarden?: BitwardenSettings;
	vault?: VaultSettings;
	doppler?: DopplerSettings;
	onepassword?: OnePasswordSettings;
	http?: HttpSettings;
}

export interface SecretSource {
	id: string;
	name: string;
	provider: SecretProvider;
	settings: SourceSettings;
	hasCredential: boolean;
	hasSetupCredential: boolean;
	bindingCount: number;
	lastTestedAt?: string;
	lastTestError?: string;
	createdAt: string;
	updatedAt?: string;
}

export interface SecretSourceCreateDto {
	name: string;
	provider: SecretProvider;
	settings: SourceSettings;
	credential?: string;
	setupCredential?: string;
}

export interface SecretSourceUpdateDto {
	name?: string;
	settings?: SourceSettings;
	credential?: string; // omitted or empty = keep stored credential
	setupCredential?: string; // omitted or empty = keep stored setup credential
	clearCredential?: boolean; // HTTP sources only: remove the stored bearer token
}

export interface SecretSourceTestDto {
	sourceId?: string;
	provider: SecretProvider;
	settings: SourceSettings;
	credential?: string;
}

export interface SecretSourceTestResult {
	ok: boolean;
	message: string;
	canBrowse: boolean;
	visibleCount: number;
}

export interface SecretBrowseQuery {
	kind: string;
	projectId?: string;
	environment?: string;
	path?: string;
	mount?: string;
	kvVersion?: number;
	vaultId?: string;
}

export interface SecretBrowseItem {
	id: string;
	name: string;
	detail?: string;
	options?: SecretBrowseItem[];
}

export interface InfisicalTarget {
	projectId: string;
	environment: string;
	secretPath: string;
	includeImports: boolean;
	expandReferences: boolean;
}

export interface BitwardenTarget {
	scope: BitwardenScope;
	id: string;
	name?: string;
}

export interface VaultTarget {
	mount: string;
	path: string;
	kvVersion: number;
}

export interface DopplerTarget {
	project?: string;
	config?: string;
}

export interface OnePasswordTarget {
	scope: OnePasswordScope;
	vaultId: string;
	itemId?: string;
	name?: string;
}

export interface HttpTarget {
	path?: string;
}

export interface BindingTarget {
	infisical?: InfisicalTarget;
	bitwarden?: BitwardenTarget;
	vault?: VaultTarget;
	doppler?: DopplerTarget;
	onepassword?: OnePasswordTarget;
	http?: HttpTarget;
}

export interface ProjectSecretBinding {
	projectId: string;
	sourceId: string;
	sourceName: string;
	provider: SecretProvider;
	target: BindingTarget;
	required: boolean;
	enabled: boolean;
	autoRedeploy: boolean;
	redeployNeeded: boolean;
	deployedAt?: string;
	lastFetchedAt?: string;
	lastCheckedAt?: string;
	lastFetchError?: string;
}

export interface ProjectSecretBindingDto {
	sourceId: string;
	target: BindingTarget;
	required: boolean;
	enabled: boolean;
	autoRedeploy: boolean;
}

export interface ProjectSecretCheckResult {
	keys: string[];
	overriddenKeys: string[];
	invalidKeys: string[];
	redeployNeeded: boolean;
	neverDeployed: boolean;
	fetchedAt: string;
	deployedAt?: string;
}

export type SetupMode = 'new-project' | 'existing-project' | 'shared-folder' | 'new-folder' | 'existing-folder' | 'kv-path';
export type SetupValues = 'import' | 'placeholder';
export type SetupEnvFile = 'keep' | 'remove';
export type SetupRemoteState = 'missing' | 'same' | 'different' | 'exists' | 'unknown';
export type SetupStepStatus = 'done' | 'skipped' | 'failed' | 'pending';

export interface SetupTarget {
	mode: SetupMode;
	projectName?: string;
	projectId?: string;
	environment?: string;
	secretPath?: string;
	folderName?: string;
	folderId?: string;
	mount?: string;
	kvVersion?: number;
}

export interface SetupPlanRequest {
	sourceId: string;
	target?: SetupTarget;
}

export interface SetupVariable {
	key: string;
	inCompose: boolean;
	composeDefault: boolean;
	composeRequired: boolean;
	inEnvFile: boolean;
	envEmpty: boolean;
	fromGit: boolean;
	secretLike: boolean;
	remote: SetupRemoteState;
}

export interface SetupIdentity {
	id: string;
	name: string;
}

export interface SetupPlan {
	provider: SecretProvider;
	suggestedProjectName: string;
	suggestedSecretPath: string;
	suggestedFolderName: string;
	canWrite: boolean;
	variables: SetupVariable[];
	projectNameTaken: boolean;
	remoteError?: string;
	deployIdentity?: SetupIdentity;
	deployIdentityError?: string;
	hasGitSource: boolean;
	alreadyBound: boolean;
}

export interface SetupApplyRequest {
	sourceId: string;
	target: SetupTarget;
	keys: string[];
	values: SetupValues;
	overwriteKeys: string[];
	envFile: SetupEnvFile;
	keepBackup: boolean;
	grantDeployIdentity: boolean;
	required: boolean;
	autoRedeploy: boolean;
}

export interface SetupStep {
	id: 'project' | 'folder' | 'secrets' | 'grant' | 'binding' | 'verify' | 'envFile';
	status: SetupStepStatus;
	detail?: string;
}

export interface SetupResult {
	ok: boolean;
	steps: SetupStep[];
	binding?: ProjectSecretBinding;
	removedKeys?: string[];
	backupFile?: string;
}

export interface ComposeService {
	name: string;
	available: string[];
	editable: boolean;
	reason?: string;
}

export interface ComposeSkippedRef {
	service: string;
	key: string;
	reason: string;
}

export interface ComposeRefsResult {
	compose: string;
	added: Record<string, string[]>;
	skipped: ComposeSkippedRef[];
}

export interface TargetKeys {
	keys: string[];
	skipped: string[];
}
