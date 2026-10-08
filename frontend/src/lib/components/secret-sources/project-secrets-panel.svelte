<script lang="ts">
	import { createQuery, useQueryClient } from '@tanstack/svelte-query';
	import { toast } from 'svelte-sonner';

	import { ArcaneButton } from '#lib/components/arcane-button/index.js';
	import SwitchWithLabel from '#lib/components/form/labeled-switch.svelte';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import { Button } from '#lib/components/ui/button/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import { Input } from '#lib/components/ui/input/index.js';
	import { Label } from '#lib/components/ui/label/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import {
		AlertIcon,
		AlertTriangleIcon,
		CheckIcon,
		FolderOpenIcon,
		LockIcon,
		RefreshIcon,
		ShieldCheckIcon
	} from '#lib/icons/index.js';
	import { m } from '#lib/paraglide/messages.js';
	import { queryKeys } from '#lib/query/query-keys.js';
	import { secretSourceService } from '#lib/services/secret-source-service.js';
	import type { ProjectSecretBinding, ProjectSecretBindingDto, ProjectSecretCheckResult } from '#lib/types/secret-source.js';
	import { handleApiResultWithCallbacks } from '#lib/utils/api.js';
	import { hasPermission } from '#lib/utils/auth.js';
	import { confirmAndRun } from '#lib/utils/bulk-actions.js';
	import { formatRelativeTime } from '#lib/utils/formatting.js';
	import { tryCatch } from '#lib/utils/try-catch.js';

	let {
		environmentId,
		projectId,
		projectName
	}: {
		environmentId: string;
		projectId: string;
		projectName: string;
	} = $props();

	const queryClient = useQueryClient();

	const canEdit = $derived(hasPermission('projects:update', environmentId));
	const canCheck = $derived(hasPermission('projects:deploy', environmentId));
	const canListSources = $derived(hasPermission('secret-sources:list') && hasPermission('secret-sources:read'));

	const bindingQuery = createQuery(() => ({
		queryKey: queryKeys.secretSources.binding(environmentId, projectId),
		queryFn: () => secretSourceService.getBinding(environmentId, projectId),
		enabled: !!environmentId && !!projectId
	}));
	const binding = $derived<ProjectSecretBinding | null>(bindingQuery.data ?? null);

	const sourcesQuery = createQuery(() => ({
		queryKey: queryKeys.secretSources.list(),
		queryFn: () => secretSourceService.list(),
		enabled: canListSources
	}));
	const sources = $derived(sourcesQuery.data ?? []);

	// ---- Editor state ----
	let editing = $state(false);
	let saving = $state(false);
	let draft = $state<ProjectSecretBindingDto>(emptyDraft());

	function emptyDraft(): ProjectSecretBindingDto {
		return {
			sourceId: '',
			remoteProjectId: '',
			environment: '',
			secretPath: '/',
			includeImports: true,
			expandReferences: true,
			required: true,
			enabled: true
		};
	}

	function startEditing() {
		draft = binding
			? {
					sourceId: binding.sourceId,
					remoteProjectId: binding.remoteProjectId,
					environment: binding.environment,
					secretPath: binding.secretPath,
					includeImports: binding.includeImports,
					expandReferences: binding.expandReferences,
					required: binding.required,
					enabled: binding.enabled
				}
			: { ...emptyDraft(), sourceId: sources.length === 1 ? (sources[0]?.id ?? '') : '' };
		editing = true;
	}

	// The Infisical project list fills the pickers. Identities without
	// permission to list projects fall back to typing the IDs.
	const activeSourceId = $derived(editing ? draft.sourceId : (binding?.sourceId ?? ''));
	const remoteProjectsQuery = createQuery(() => ({
		queryKey: queryKeys.secretSources.remoteProjects(activeSourceId),
		queryFn: () => secretSourceService.listRemoteProjects(activeSourceId),
		enabled: !!activeSourceId && canListSources,
		retry: false
	}));
	const remoteProjects = $derived(remoteProjectsQuery.data ?? []);
	const manualEntry = $derived(remoteProjectsQuery.isError || (remoteProjectsQuery.isSuccess && remoteProjects.length === 0));
	const selectedRemoteProject = $derived(remoteProjects.find((project) => project.id === draft.remoteProjectId));
	const boundRemoteProject = $derived(remoteProjects.find((project) => project.id === binding?.remoteProjectId));

	const foldersQuery = createQuery(() => ({
		queryKey: queryKeys.secretSources.remoteFolders(
			draft.sourceId,
			draft.remoteProjectId,
			draft.environment,
			draft.secretPath ?? '/'
		),
		queryFn: () =>
			secretSourceService.listRemoteFolders(draft.sourceId, draft.remoteProjectId, draft.environment, draft.secretPath ?? '/'),
		enabled: editing && !!draft.sourceId && !!draft.remoteProjectId && !!draft.environment,
		retry: false
	}));

	function normalizePath(path: string): string {
		const trimmed = path.trim().replace(/^\/+|\/+$/g, '');
		return trimmed ? `/${trimmed}` : '/';
	}

	function enterFolder(folder: string) {
		const base = normalizePath(draft.secretPath ?? '/');
		draft.secretPath = base === '/' ? `/${folder}` : `${base}/${folder}`;
	}

	function leaveFolder() {
		const parts = normalizePath(draft.secretPath ?? '/')
			.split('/')
			.filter(Boolean);
		parts.pop();
		draft.secretPath = parts.length ? `/${parts.join('/')}` : '/';
	}

	const draftValid = $derived(!!draft.sourceId && !!draft.remoteProjectId.trim() && !!draft.environment.trim());

	async function invalidateBinding() {
		await queryClient.invalidateQueries({ queryKey: queryKeys.secretSources.binding(environmentId, projectId) });
		await queryClient.invalidateQueries({ queryKey: queryKeys.secretSources.list() });
	}

	async function save() {
		if (!draftValid) return;
		await handleApiResultWithCallbacks({
			result: await tryCatch(
				secretSourceService.saveBinding(environmentId, projectId, {
					...draft,
					secretPath: normalizePath(draft.secretPath ?? '/')
				})
			),
			message: m.project_secrets_save_failed(),
			setLoadingState: (value) => (saving = value),
			onSuccess: async () => {
				toast.success(m.project_secrets_saved());
				editing = false;
				checkResult = null;
				await invalidateBinding();
			}
		});
	}

	function detach() {
		confirmAndRun({
			title: m.project_secrets_detach_title({ name: projectName }),
			message: m.project_secrets_detach_message(),
			confirmLabel: m.project_secrets_detach(),
			destructive: true,
			run: () => secretSourceService.deleteBinding(environmentId, projectId),
			failureMessage: m.common_action_failed(),
			onSuccess: async () => {
				toast.success(m.project_secrets_detached());
				checkResult = null;
				await invalidateBinding();
			}
		});
	}

	// ---- Check ----
	let checking = $state(false);
	let checkResult = $state<ProjectSecretCheckResult | null>(null);

	async function checkNow() {
		await handleApiResultWithCallbacks({
			result: await tryCatch(secretSourceService.checkBinding(environmentId, projectId)),
			message: m.project_secrets_check_failed(),
			setLoadingState: (value) => (checking = value),
			onSuccess: (result) => {
				checkResult = result;
			},
			onError: async () => {
				await invalidateBinding();
			}
		});
		await queryClient.invalidateQueries({ queryKey: queryKeys.secretSources.binding(environmentId, projectId) });
	}

	const overridden = $derived(new Set(checkResult?.overriddenKeys ?? []));
</script>

{#if environmentId !== '0'}
	<Alert.Root variant="info" icon={AlertIcon} heading={m.project_secrets_local_only()} />
{:else if bindingQuery.isPending}
	<div class="flex justify-center py-12"><Spinner /></div>
{:else if editing}
	{@render editor()}
{:else if !binding}
	<div class="rounded-xl border border-dashed border-border/70">
		<Empty.Root>
			<Empty.Header>
				<Empty.Media variant="icon">
					<LockIcon />
				</Empty.Media>
				<Empty.Title>{m.project_secrets_not_bound_title()}</Empty.Title>
				<Empty.Description>{m.project_secrets_not_bound_description()}</Empty.Description>
			</Empty.Header>
			{#if canEdit}
				<Empty.Content>
					{#if canListSources && sourcesQuery.isSuccess && sources.length === 0}
						<p class="text-sm text-muted-foreground">{m.project_secrets_no_sources()}</p>
						<ArcaneButton
							action="base"
							tone="outline-primary"
							href="/customize/secret-sources"
							icon={LockIcon}
							customLabel={m.secret_sources_title()}
						/>
					{:else}
						<ArcaneButton
							action="base"
							tone="outline-primary"
							icon={LockIcon}
							customLabel={m.project_secrets_bind()}
							disabled={!canListSources}
							onclick={startEditing}
						/>
					{/if}
				</Empty.Content>
			{/if}
		</Empty.Root>
	</div>
{:else}
	{@render summary(binding)}
{/if}

{#snippet summary(current: ProjectSecretBinding)}
	<div class="space-y-4">
		<div class="flex flex-wrap items-center gap-4 rounded-xl border border-border/70 bg-card/60 p-4 backdrop-blur-md">
			<div class="flex size-12 shrink-0 items-center justify-center rounded-full bg-primary/10 ring-1 ring-primary/30 ring-inset">
				<ShieldCheckIcon class="size-6 text-primary" />
			</div>
			<div class="min-w-0 flex-1 space-y-1">
				<div class="flex flex-wrap items-center gap-2">
					<h3 class="text-base font-semibold">{m.project_secrets_title()}</h3>
					{#if !current.enabled}
						<Badge variant="gray" size="sm">{m.project_secrets_disabled()}</Badge>
					{:else if current.required}
						<Badge variant="blue" size="sm">{m.project_secrets_required()}</Badge>
					{:else}
						<Badge variant="amber" size="sm">{m.project_secrets_optional()}</Badge>
					{/if}
				</div>
				<p class="truncate font-mono text-xs text-muted-foreground">
					{current.sourceName} · {boundRemoteProject?.name ?? current.remoteProjectId} · {current.environment} · {current.secretPath}
				</p>
				<p class="text-xs text-muted-foreground">
					{#if current.lastFetchedAt}
						{m.project_secrets_last_fetched({ time: formatRelativeTime(current.lastFetchedAt) })}
					{:else}
						{m.project_secrets_never_fetched()}
					{/if}
				</p>
			</div>
			<div class="flex items-center gap-2">
				{#if canCheck}
					<ArcaneButton
						action="refresh"
						tone="outline-primary"
						size="sm"
						icon={RefreshIcon}
						customLabel={m.project_secrets_check_now()}
						loading={checking}
						disabled={checking}
						onclick={checkNow}
					/>
				{/if}
				{#if canEdit}
					<ArcaneButton action="edit" tone="outline" size="sm" onclick={startEditing} disabled={!canListSources} />
					<ArcaneButton
						action="remove"
						tone="outline-destructive"
						size="sm"
						customLabel={m.project_secrets_detach()}
						onclick={detach}
					/>
				{/if}
			</div>
		</div>

		{#if current.lastFetchError}
			<Alert.Root
				variant="destructive-subtle"
				icon={AlertIcon}
				heading={m.project_secrets_last_fetch_error()}
				description={current.lastFetchError}
			/>
		{/if}

		{#if checkResult}
			{@render checkDetails(checkResult)}
		{:else}
			<p class="text-sm text-muted-foreground">{m.project_secrets_check_hint()}</p>
		{/if}

		<Alert.Root
			variant="primary-subtle"
			icon={LockIcon}
			description={m.project_secrets_usage_hint({ example: 'environment: [DB_PASSWORD]' })}
		/>
	</div>
{/snippet}

{#snippet checkDetails(result: ProjectSecretCheckResult)}
	<div class="space-y-3 rounded-xl border border-border/70 bg-card/60 p-4">
		<div class="flex flex-wrap items-center gap-2">
			{#if result.neverDeployed}
				<Badge variant="gray" size="sm">{m.project_secrets_never_deployed()}</Badge>
			{:else if result.redeployNeeded}
				<Badge variant="amber" size="sm"><AlertTriangleIcon class="size-3" />{m.project_secrets_redeploy_needed()}</Badge>
				<span class="text-xs text-muted-foreground">{m.project_secrets_redeploy_needed_description()}</span>
			{:else}
				<Badge variant="green" size="sm"><CheckIcon class="size-3" />{m.project_secrets_in_sync()}</Badge>
			{/if}
			<span class="ml-auto text-xs text-muted-foreground">{m.project_secrets_keys_count({ count: result.keys.length })}</span>
		</div>

		{#if result.keys.length > 0}
			<div class="flex flex-wrap gap-1.5">
				{#each result.keys as key (key)}
					<Badge
						variant={overridden.has(key) ? 'amber' : 'outline'}
						size="sm"
						mono
						title={overridden.has(key) ? m.project_secrets_overrides() : undefined}
					>
						{key}
					</Badge>
				{/each}
			</div>
		{/if}

		{#if result.overriddenKeys.length > 0}
			<Alert.Root
				variant="warning-subtle"
				icon={AlertTriangleIcon}
				heading={m.project_secrets_overrides()}
				description={m.project_secrets_overrides_description()}
			/>
		{/if}

		{#if result.invalidKeys.length > 0}
			<Alert.Root
				variant="warning-subtle"
				icon={AlertTriangleIcon}
				heading={m.project_secrets_invalid_keys()}
				description={`${m.project_secrets_invalid_keys_description()} ${result.invalidKeys.join(', ')}`}
			/>
		{/if}
	</div>
{/snippet}

{#snippet editor()}
	<form
		class="grid max-w-2xl gap-4 rounded-xl border border-border/70 bg-card/60 p-4"
		onsubmit={(event) => {
			event.preventDefault();
			void save();
		}}
	>
		<h3 class="text-base font-semibold">{m.project_secrets_title()}</h3>

		<div class="space-y-2">
			<Label for="secret-source">{m.project_secrets_source()}</Label>
			<Select.Root
				type="single"
				value={draft.sourceId}
				onValueChange={(value) => {
					draft = { ...draft, sourceId: value, remoteProjectId: '', environment: '', secretPath: '/' };
				}}
			>
				<Select.Trigger id="secret-source" class="w-full">
					<span>{sources.find((source) => source.id === draft.sourceId)?.name ?? m.project_secrets_select_source()}</span>
				</Select.Trigger>
				<Select.Content>
					{#each sources as source (source.id)}
						<Select.Item value={source.id}>{source.name}</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>
		</div>

		{#if draft.sourceId}
			{#if remoteProjectsQuery.isPending}
				<div class="flex items-center gap-2 text-sm text-muted-foreground"><Spinner class="size-4" />{m.common_loading()}</div>
			{:else if manualEntry}
				<Alert.Root variant="info" icon={AlertIcon} description={m.project_secrets_enter_ids_description()} />
				<div class="grid gap-4 sm:grid-cols-2">
					<div class="space-y-2">
						<Label for="secret-remote-project">{m.project_secrets_infisical_project_id()}</Label>
						<Input id="secret-remote-project" mono bind:value={draft.remoteProjectId} />
					</div>
					<div class="space-y-2">
						<Label for="secret-environment">{m.project_secrets_environment_slug()}</Label>
						<Input id="secret-environment" mono placeholder="prod" bind:value={draft.environment} />
					</div>
				</div>
			{:else}
				<div class="grid gap-4 sm:grid-cols-2">
					<div class="space-y-2">
						<Label for="secret-remote-project">{m.project_secrets_infisical_project()}</Label>
						<Select.Root
							type="single"
							value={draft.remoteProjectId}
							onValueChange={(value) => {
								const project = remoteProjects.find((candidate) => candidate.id === value);
								const environment = project?.environments.length === 1 ? (project.environments[0]?.slug ?? '') : '';
								draft = { ...draft, remoteProjectId: value, environment, secretPath: '/' };
							}}
						>
							<Select.Trigger id="secret-remote-project" class="w-full">
								<span>{selectedRemoteProject?.name ?? m.project_secrets_select_project()}</span>
							</Select.Trigger>
							<Select.Content>
								{#each remoteProjects as project (project.id)}
									<Select.Item value={project.id}>{project.name}</Select.Item>
								{/each}
							</Select.Content>
						</Select.Root>
					</div>
					<div class="space-y-2">
						<Label for="secret-environment">{m.project_secrets_environment()}</Label>
						<Select.Root
							type="single"
							value={draft.environment}
							disabled={!selectedRemoteProject}
							onValueChange={(value) => {
								draft = { ...draft, environment: value, secretPath: '/' };
							}}
						>
							<Select.Trigger id="secret-environment" class="w-full">
								<span>
									{selectedRemoteProject?.environments.find((environment) => environment.slug === draft.environment)?.name ??
										m.project_secrets_select_environment()}
								</span>
							</Select.Trigger>
							<Select.Content>
								{#each selectedRemoteProject?.environments ?? [] as environment (environment.slug)}
									<Select.Item value={environment.slug}>{environment.name}</Select.Item>
								{/each}
							</Select.Content>
						</Select.Root>
					</div>
				</div>
			{/if}

			{#if draft.remoteProjectId && draft.environment}
				<div class="space-y-2">
					<Label for="secret-path">{m.project_secrets_path()}</Label>
					<Input id="secret-path" mono bind:value={draft.secretPath} />
					<div class="flex flex-wrap items-center gap-1.5">
						{#if normalizePath(draft.secretPath ?? '/') !== '/'}
							<Button type="button" size="sm" variant="ghost" class="h-7" onclick={leaveFolder}
								><span class="font-mono">..</span></Button
							>
						{/if}
						{#if foldersQuery.isPending}
							<Spinner class="size-4" />
						{:else}
							{#each foldersQuery.data ?? [] as folder (folder)}
								<Button type="button" size="sm" variant="outline" class="h-7" onclick={() => enterFolder(folder)}>
									<FolderOpenIcon class="size-3.5" />
									<span class="font-mono">{folder}</span>
								</Button>
							{/each}
						{/if}
					</div>
				</div>
			{/if}
		{/if}

		<div class="grid gap-3 border-t border-border/50 pt-4">
			<SwitchWithLabel
				id="secret-required"
				label={m.project_secrets_required()}
				description={m.project_secrets_required_description()}
				bind:checked={draft.required}
			/>
			<SwitchWithLabel
				id="secret-enabled"
				label={m.common_enabled()}
				description={m.project_secrets_enabled_description()}
				bind:checked={draft.enabled}
			/>
			<SwitchWithLabel id="secret-imports" label={m.project_secrets_include_imports()} bind:checked={draft.includeImports} />
			<SwitchWithLabel
				id="secret-references"
				label={m.project_secrets_expand_references()}
				bind:checked={draft.expandReferences}
			/>
		</div>

		<div class="flex gap-2">
			<ArcaneButton action="cancel" tone="outline" type="button" onclick={() => (editing = false)} disabled={saving} />
			<ArcaneButton action="save" type="submit" loading={saving} disabled={saving || !draftValid} />
		</div>
	</form>
{/snippet}
