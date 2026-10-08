export interface SecretSource {
	id: string;
	name: string;
	provider: 'infisical';
	siteUrl: string;
	clientId: string;
	organizationSlug?: string;
	hasClientSecret: boolean;
	bindingCount: number;
	lastTestedAt?: string;
	lastTestError?: string;
	createdAt: string;
	updatedAt?: string;
}

export interface SecretSourceCreateDto {
	name: string;
	siteUrl?: string;
	clientId: string;
	clientSecret: string;
	organizationSlug?: string;
}

export interface SecretSourceUpdateDto {
	name?: string;
	siteUrl?: string;
	clientId?: string;
	clientSecret?: string; // omitted or empty = keep stored secret
	organizationSlug?: string;
}

export interface SecretSourceTestDto {
	sourceId?: string;
	siteUrl?: string;
	clientId: string;
	clientSecret?: string;
	organizationSlug?: string;
}

export interface SecretSourceTestResult {
	ok: boolean;
	message: string;
	tokenTtlSeconds?: number;
	projectsVisible: number;
	canListProjects: boolean;
}

export interface RemoteSecretEnvironment {
	name: string;
	slug: string;
}

export interface RemoteSecretProject {
	id: string;
	name: string;
	slug: string;
	environments: RemoteSecretEnvironment[];
}

export interface ProjectSecretBinding {
	projectId: string;
	sourceId: string;
	sourceName: string;
	remoteProjectId: string;
	environment: string;
	secretPath: string;
	includeImports: boolean;
	expandReferences: boolean;
	required: boolean;
	enabled: boolean;
	deployedAt?: string;
	lastFetchedAt?: string;
	lastFetchError?: string;
}

export interface ProjectSecretBindingDto {
	sourceId: string;
	remoteProjectId: string;
	environment: string;
	secretPath?: string;
	includeImports: boolean;
	expandReferences: boolean;
	required: boolean;
	enabled: boolean;
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
