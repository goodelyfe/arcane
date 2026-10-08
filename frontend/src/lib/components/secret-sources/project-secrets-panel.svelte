<script lang="ts">
	import { invalidateAll } from '$app/navigation';
	import { createQuery, useQueryClient } from '@tanstack/svelte-query';
	import { toast } from 'svelte-sonner';

	import { ArcaneButton } from '#lib/components/arcane-button/index.js';
	import SwitchWithLabel from '#lib/components/form/labeled-switch.svelte';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import { Label } from '#lib/components/ui/label/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import { Spinner } from '#lib/components/ui/spinner/index.js';
	import {
		AlertIcon,
		AlertTriangleIcon,
		CheckIcon,
		LockIcon,
		RefreshIcon,
		ShieldCheckIcon,
		VariableIcon,
		ZapIcon
	} from '#lib/icons/index.js';
	import { m } from '#lib/paraglide/messages.js';
	import { queryKeys } from '#lib/query/query-keys.js';
	import { projectService } from '#lib/services/project-service.js';
	import { secretSourceService } from '#lib/services/secret-source-service.js';
	import type {
		BitwardenTarget,
		ComposeRefsResult,
		InfisicalTarget,
		ProjectSecretBinding,
		ProjectSecretCheckResult,
		SecretProvider
	} from '#lib/types/secret-source.js';
	import { extractApiErrorMessage, handleApiResultWithCallbacks } from '#lib/utils/api.js';
	import { hasPermission } from '#lib/utils/auth.js';
	import { confirmAndRun } from '#lib/utils/bulk-actions.js';
	import { formatRelativeTime } from '#lib/utils/formatting.js';
	import { tryCatch } from '#lib/utils/try-catch.js';

	import BitwardenTargetFields from './bitwarden-target-fields.svelte';
	import ComposeRefsSheet from './compose-refs-sheet.svelte';
	import InfisicalTargetFields from './infisical-target-fields.svelte';
	import SecretSetupSheet from './secret-setup-sheet.svelte';

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
	// Setup can write to Infisical (with a setup identity) and to Bitwarden through bw serve.
	const setupSources = $derived(sources.filter((source) => source.provider === 'infisical' || source.provider === 'bitwarden'));
	const canSetup = $derived(canEdit && hasPermission('secret-sources:update'));

	// ---- Guided setup ----
	let setupOpen = $state(false);
	let setupSession = $state(0);

	function openSetup() {
		setupSession += 1;
		setupOpen = true;
	}

	// ---- Compose references ----
	let composeRefsOpen = $state(false);
	let composeRefsSession = $state(0);
	let composeRefsLoading = $state(false);
	let composeRefsCompose = $state('');
	let composeRefsKeys = $state<string[]>([]);

	// Opens the picker for keys, or for every key of the current binding.
	async function openComposeRefs(keys?: string[]) {
		composeRefsLoading = true;
		const projectResult = await tryCatch(projectService.getProjectForEnvironment(environmentId, projectId));
		let keyList = keys;
		let keyError: unknown = null;
		if (!keyList && binding) {
			const keysResult = await tryCatch(secretSourceService.targetKeys(binding.sourceId, binding.target));
			keyList = keysResult.data?.keys;
			keyError = keysResult.error;
		}
		composeRefsLoading = false;
		if (projectResult.error || keyError || !keyList) {
			toast.error(m.compose_refs_load_failed(), {
				description: extractApiErrorMessage(projectResult.error ?? keyError)
			});
			return;
		}
		composeRefsCompose = projectResult.data.composeContent ?? '';
		composeRefsKeys = keyList;
		composeRefsSession += 1;
		composeRefsOpen = true;
	}

	async function saveComposeRefs(result: ComposeRefsResult) {
		const added = Object.values(result.added).reduce((total, keys) => total + keys.length, 0);
		if (added === 0) return;
		const saved = await tryCatch(projectService.updateProject(projectId, undefined, result.compose));
		if (saved.error) {
			toast.error(m.compose_refs_failed(), { description: extractApiErrorMessage(saved.error) });
			return;
		}
		toast.success(m.compose_refs_saved({ count: added }));
		await invalidateAll();
	}

	// ---- Editor state ----
	let editing = $state(false);
	let saving = $state(false);
	let sourceId = $state('');
	let required = $state(true);
	let enabled = $state(true);
	let autoRedeploy = $state(false);
	let infisicalTarget = $state<InfisicalTarget>(emptyInfisicalTarget());
	let bitwardenTarget = $state<BitwardenTarget>(emptyBitwardenTarget());

	function emptyInfisicalTarget(): InfisicalTarget {
		return { projectId: '', environment: '', secretPath: '/', includeImports: true, expandReferences: true };
	}

	function emptyBitwardenTarget(): BitwardenTarget {
		return { scope: 'folder', id: '', name: '' };
	}

	const draftProvider = $derived<SecretProvider | undefined>(sources.find((source) => source.id === sourceId)?.provider);

	function startEditing() {
		sourceId = binding?.sourceId ?? (sources.length === 1 ? (sources[0]?.id ?? '') : '');
		required = binding?.required ?? true;
		enabled = binding?.enabled ?? true;
		autoRedeploy = binding?.autoRedeploy ?? false;
		infisicalTarget = binding?.target.infisical ? { ...binding.target.infisical } : emptyInfisicalTarget();
		bitwardenTarget = binding?.target.bitwarden ? { ...binding.target.bitwarden } : emptyBitwardenTarget();
		editing = true;
	}

	function selectSource(value: string) {
		sourceId = value;
		infisicalTarget = emptyInfisicalTarget();
		bitwardenTarget = emptyBitwardenTarget();
	}

	const draftValid = $derived.by(() => {
		if (!sourceId) return false;
		if (draftProvider === 'infisical') return !!infisicalTarget.projectId.trim() && !!infisicalTarget.environment.trim();
		if (draftProvider === 'bitwarden') return !!bitwardenTarget.id;
		return false;
	});

	// Shows the Infisical project name instead of its ID in the summary.
	const boundProjectsQuery = createQuery(() => ({
		queryKey: queryKeys.secretSources.browse(binding?.sourceId ?? '', 'projects'),
		queryFn: () => secretSourceService.browse(binding?.sourceId ?? '', { kind: 'projects' }),
		enabled: !editing && binding?.provider === 'infisical' && canListSources,
		retry: false
	}));

	function describeTarget(current: ProjectSecretBinding): string {
		const infisical = current.target.infisical;
		if (infisical) {
			const projectName = boundProjectsQuery.data?.find((project) => project.id === infisical.projectId)?.name;
			return `${projectName ?? infisical.projectId} · ${infisical.environment} · ${infisical.secretPath}`;
		}
		const bitwarden = current.target.bitwarden;
		if (bitwarden) {
			return `${scopeLabel(bitwarden.scope)} · ${bitwarden.name || bitwarden.id}`;
		}
		return '';
	}

	function scopeLabel(scope: BitwardenTarget['scope']): string {
		switch (scope) {
			case 'collection':
				return m.bitwarden_scope_collection();
			case 'item':
				return m.bitwarden_scope_item();
			default:
				return m.bitwarden_scope_folder();
		}
	}

	function providerLabel(provider: SecretProvider | undefined): string {
		return provider === 'bitwarden' ? m.secret_sources_provider_bitwarden() : m.secret_sources_provider_infisical();
	}

	async function invalidateBinding() {
		await queryClient.invalidateQueries({ queryKey: queryKeys.secretSources.binding(environmentId, projectId) });
		await queryClient.invalidateQueries({ queryKey: queryKeys.secretSources.list() });
	}

	async function save() {
		if (!draftValid) return;
		await handleApiResultWithCallbacks({
			result: await tryCatch(
				secretSourceService.saveBinding(environmentId, projectId, {
					sourceId,
					target: draftProvider === 'bitwarden' ? { bitwarden: bitwardenTarget } : { infisical: infisicalTarget },
					required,
					enabled,
					autoRedeploy
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
						<div class="flex flex-wrap justify-center gap-2">
							{#if canSetup && setupSources.length > 0}
								<ArcaneButton
									action="base"
									icon={ZapIcon}
									customLabel={m.secret_setup_open()}
									disabled={!canListSources}
									onclick={openSetup}
								/>
							{/if}
							<ArcaneButton
								action="base"
								tone="outline-primary"
								icon={LockIcon}
								customLabel={m.project_secrets_bind()}
								disabled={!canListSources}
								onclick={startEditing}
							/>
						</div>
					{/if}
				</Empty.Content>
			{/if}
		</Empty.Root>
	</div>
{:else}
	{@render summary(binding)}
{/if}

{#if setupSession > 0}
	{#key setupSession}
		<SecretSetupSheet
			bind:open={setupOpen}
			{environmentId}
			{projectId}
			sources={setupSources}
			onDone={async () => {
				checkResult = null;
				await invalidateBinding();
			}}
			onAddToCompose={(keys) => {
				setupOpen = false;
				void openComposeRefs(keys);
			}}
		/>
	{/key}
{/if}

{#if composeRefsSession > 0}
	{#key composeRefsSession}
		<ComposeRefsSheet bind:open={composeRefsOpen} compose={composeRefsCompose} keys={composeRefsKeys} onApply={saveComposeRefs} />
	{/key}
{/if}

{#snippet summary(current: ProjectSecretBinding)}
	<div class="space-y-4">
		<div class="flex flex-wrap items-center gap-4 rounded-xl border border-border/70 bg-card/60 p-4 backdrop-blur-md">
			<div class="flex size-12 shrink-0 items-center justify-center rounded-full bg-primary/10 ring-1 ring-primary/30 ring-inset">
				<ShieldCheckIcon class="size-6 text-primary" />
			</div>
			<div class="min-w-0 flex-1 space-y-1">
				<div class="flex flex-wrap items-center gap-2">
					<h3 class="text-base font-semibold">{m.project_secrets_title({ provider: providerLabel(current.provider) })}</h3>
					{#if !current.enabled}
						<Badge variant="gray" size="sm">{m.project_secrets_disabled()}</Badge>
					{:else if current.required}
						<Badge variant="blue" size="sm">{m.project_secrets_required()}</Badge>
					{:else}
						<Badge variant="amber" size="sm">{m.project_secrets_optional()}</Badge>
					{/if}
					{#if current.autoRedeploy}
						<Badge variant="violet" size="sm">{m.project_secrets_auto_redeploy()}</Badge>
					{/if}
					{#if current.redeployNeeded}
						<Badge variant="amber" size="sm"><AlertTriangleIcon class="size-3" />{m.project_secrets_redeploy_needed()}</Badge>
					{/if}
				</div>
				<p class="truncate font-mono text-xs text-muted-foreground">
					{current.sourceName} · {describeTarget(current)}
				</p>
				<p class="text-xs text-muted-foreground">
					{#if current.lastFetchedAt}
						{m.project_secrets_last_fetched({ time: formatRelativeTime(current.lastFetchedAt) })}
					{:else}
						{m.project_secrets_never_fetched()}
					{/if}
					{#if current.lastCheckedAt}
						· {m.project_secrets_last_checked({ time: formatRelativeTime(current.lastCheckedAt) })}
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
					{#if canSetup && setupSources.length > 0}
						<ArcaneButton
							action="base"
							tone="outline"
							size="sm"
							icon={ZapIcon}
							customLabel={m.secret_setup_open_again()}
							disabled={!canListSources}
							onclick={openSetup}
						/>
					{/if}
					<ArcaneButton
						action="base"
						tone="outline"
						size="sm"
						icon={VariableIcon}
						customLabel={m.compose_refs_open()}
						loading={composeRefsLoading}
						disabled={composeRefsLoading}
						onclick={() => void openComposeRefs()}
					/>
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

		{#if result.keys.length === 0}
			<Alert.Root
				variant="destructive-subtle"
				icon={AlertIcon}
				heading={m.project_secrets_empty_title()}
				description={binding?.required ? m.project_secrets_empty_required() : m.project_secrets_empty_optional()}
			/>
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
		<h3 class="text-base font-semibold">{m.project_secrets_title({ provider: providerLabel(draftProvider) })}</h3>

		<div class="space-y-2">
			<Label for="secret-source">{m.project_secrets_source()}</Label>
			<Select.Root type="single" value={sourceId} onValueChange={selectSource}>
				<Select.Trigger id="secret-source" class="w-full">
					<span>{sources.find((source) => source.id === sourceId)?.name ?? m.project_secrets_select_source()}</span>
				</Select.Trigger>
				<Select.Content>
					{#each sources as source (source.id)}
						<Select.Item value={source.id}>
							<div class="flex flex-col">
								<span>{source.name}</span>
								<span class="text-xs text-muted-foreground">{providerLabel(source.provider)}</span>
							</div>
						</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>
		</div>

		{#if sourceId && draftProvider === 'infisical'}
			{#key sourceId}
				<InfisicalTargetFields {sourceId} bind:target={infisicalTarget} />
			{/key}
		{:else if sourceId && draftProvider === 'bitwarden'}
			{#key sourceId}
				<BitwardenTargetFields {sourceId} bind:target={bitwardenTarget} />
			{/key}
		{/if}

		<div class="grid gap-3 border-t border-border/50 pt-4">
			<SwitchWithLabel
				id="secret-required"
				label={m.project_secrets_required()}
				description={m.project_secrets_required_description()}
				bind:checked={required}
			/>
			<SwitchWithLabel
				id="secret-enabled"
				label={m.common_enabled()}
				description={m.project_secrets_enabled_description()}
				bind:checked={enabled}
			/>
			<SwitchWithLabel
				id="secret-auto-redeploy"
				label={m.project_secrets_auto_redeploy()}
				description={m.project_secrets_auto_redeploy_description()}
				bind:checked={autoRedeploy}
			/>
		</div>

		<div class="flex gap-2">
			<ArcaneButton action="cancel" tone="outline" type="button" onclick={() => (editing = false)} disabled={saving} />
			<ArcaneButton action="save" type="submit" loading={saving} disabled={saving || !draftValid} />
		</div>
	</form>
{/snippet}
