export type SecretProvider = 'infisical' | 'bitwarden';

export type BitwardenScope = 'folder' | 'collection' | 'item';

export interface InfisicalSettings {
	siteUrl: string;
	clientId: string;
	organizationSlug?: string;
}

export interface BitwardenSettings {
	serveUrl: string;
}

export interface SourceSettings {
	infisical?: InfisicalSettings;
	bitwarden?: BitwardenSettings;
}

export interface SecretSource {
	id: string;
	name: string;
	provider: SecretProvider;
	settings: SourceSettings;
	hasCredential: boolean;
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
}

export interface SecretSourceUpdateDto {
	name?: string;
	settings?: SourceSettings;
	credential?: string; // omitted or empty = keep stored credential
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

export interface BindingTarget {
	infisical?: InfisicalTarget;
	bitwarden?: BitwardenTarget;
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
