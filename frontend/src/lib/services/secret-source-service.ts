import type {
	ProjectSecretBinding,
	ProjectSecretBindingDto,
	ProjectSecretCheckResult,
	RemoteSecretProject,
	SecretSource,
	SecretSourceCreateDto,
	SecretSourceTestDto,
	SecretSourceTestResult,
	SecretSourceUpdateDto
} from '#lib/types/secret-source.js';

import BaseAPIService from './api-service';

class SecretSourceService extends BaseAPIService {
	async list(): Promise<SecretSource[]> {
		const response = await this.api.get('/secret-sources');
		return response.data?.data ?? [];
	}

	async create(dto: SecretSourceCreateDto): Promise<SecretSource> {
		return this.handleResponse(this.api.post('/secret-sources', dto));
	}

	async update(id: string, dto: SecretSourceUpdateDto): Promise<SecretSource> {
		return this.handleResponse(this.api.put(`/secret-sources/${encodeURIComponent(id)}`, dto));
	}

	async delete(id: string): Promise<void> {
		return this.handleResponse(this.api.delete(`/secret-sources/${encodeURIComponent(id)}`));
	}

	async test(dto: SecretSourceTestDto): Promise<SecretSourceTestResult> {
		return this.handleResponse(this.api.post('/secret-sources/test', dto));
	}

	async listRemoteProjects(id: string): Promise<RemoteSecretProject[]> {
		const response = await this.api.get(`/secret-sources/${encodeURIComponent(id)}/projects`);
		return response.data?.data ?? [];
	}

	async listRemoteFolders(id: string, projectId: string, environment: string, path: string): Promise<string[]> {
		const response = await this.api.get(`/secret-sources/${encodeURIComponent(id)}/folders`, {
			params: { projectId, environment, path }
		});
		return response.data?.data ?? [];
	}

	async getBinding(environmentId: string, projectId: string): Promise<ProjectSecretBinding | null> {
		const response = await this.api.get(this.bindingPath(environmentId, projectId));
		return response.data?.data ?? null;
	}

	async saveBinding(environmentId: string, projectId: string, dto: ProjectSecretBindingDto): Promise<ProjectSecretBinding> {
		return this.handleResponse(this.api.put(this.bindingPath(environmentId, projectId), dto));
	}

	async deleteBinding(environmentId: string, projectId: string): Promise<void> {
		return this.handleResponse(this.api.delete(this.bindingPath(environmentId, projectId)));
	}

	async checkBinding(environmentId: string, projectId: string): Promise<ProjectSecretCheckResult> {
		return this.handleResponse(this.api.post(`${this.bindingPath(environmentId, projectId)}/check`, {}));
	}

	private bindingPath(environmentId: string, projectId: string): string {
		return `/environments/${encodeURIComponent(environmentId)}/projects/${encodeURIComponent(projectId)}/secrets`;
	}
}

export const secretSourceService = new SecretSourceService();
