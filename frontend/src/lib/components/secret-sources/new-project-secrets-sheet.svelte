<script lang="ts">
	import { createQuery } from '@tanstack/svelte-query';
	import { SvelteSet } from 'svelte/reactivity';

	import { ArcaneButton } from '#lib/components/arcane-button/index.js';
	import SwitchWithLabel from '#lib/components/form/labeled-switch.svelte';
	import SheetFooterActions from '#lib/components/sheets/sheet-footer-actions.svelte';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import { Label } from '#lib/components/ui/label/index.js';
	import * as ResponsiveDialog from '#lib/components/ui/responsive-dialog/index.js';
	import * as Select from '#lib/components/ui/select/index.js';
	import { AlertIcon, RefreshIcon } from '#lib/icons/index.js';
	import { m } from '#lib/paraglide/messages.js';
	import { queryKeys } from '#lib/query/query-keys.js';
	import { secretSourceService } from '#lib/services/secret-source-service.js';
	import type { BindingTarget, BitwardenTarget, InfisicalTarget, SecretProvider } from '#lib/types/secret-source.js';
	import { extractApiErrorMessage } from '#lib/utils/api.js';
	import { tryCatch } from '#lib/utils/try-catch.js';

	import BitwardenTargetFields from './bitwarden-target-fields.svelte';
	import ComposeRefsPicker from './compose-refs-picker.svelte';
	import { type NewProjectSecrets, selectionToAssignments } from './compose-refs.js';
	import InfisicalTargetFields from './infisical-target-fields.svelte';

	let {
		open = $bindable(false),
		compose,
		onApply
	}: {
		open: boolean;
		compose: string;
		// Receives the compose content with references added and the binding to
		// create once the project exists.
		onApply: (compose: string, secrets: NewProjectSecrets) => void;
	} = $props();

	const sourcesQuery = createQuery(() => ({
		queryKey: queryKeys.secretSources.list(),
		queryFn: () => secretSourceService.list(),
		enabled: open
	}));
	const sources = $derived(sourcesQuery.data ?? []);

	let sourceId = $state('');
	let infisicalTarget = $state<InfisicalTarget>({
		projectId: '',
		environment: '',
		secretPath: '/',
		includeImports: true,
		expandReferences: true
	});
	let bitwardenTarget = $state<BitwardenTarget>({ scope: 'folder', id: '', name: '' });
	let required = $state(true);
	let autoRedeploy = $state(false);

	const provider = $derived<SecretProvider | undefined>(sources.find((source) => source.id === sourceId)?.provider);
	const target = $derived<BindingTarget | null>(
		provider === 'infisical' && infisicalTarget.projectId && infisicalTarget.environment
			? { infisical: infisicalTarget }
			: provider === 'bitwarden' && bitwardenTarget.id
				? { bitwarden: bitwardenTarget }
				: null
	);

	let keys = $state<string[] | null>(null);
	let loadingKeys = $state(false);
	let error = $state<string | null>(null);
	let applying = $state(false);
	let pickerSession = $state(0);
	const selection = new SvelteSet<string>();

	async function loadKeys() {
		if (!target) return;
		loadingKeys = true;
		error = null;
		const result = await tryCatch(secretSourceService.targetKeys(sourceId, target));
		loadingKeys = false;
		if (result.error) {
			error = extractApiErrorMessage(result.error);
			return;
		}
		keys = result.data.keys;
		selection.clear();
		pickerSession += 1;
	}

	async function apply() {
		if (!target || keys === null) return;
		applying = true;
		error = null;
		let updated = compose;
		if (selection.size > 0) {
			const result = await tryCatch(secretSourceService.addComposeRefs(compose, selectionToAssignments(selection)));
			if (result.error) {
				applying = false;
				error = extractApiErrorMessage(result.error);
				return;
			}
			updated = result.data.compose;
		}
		applying = false;
		onApply(updated, {
			binding: { sourceId, target, required, enabled: true, autoRedeploy },
			sourceName: sources.find((source) => source.id === sourceId)?.name ?? '',
			keyCount: keys.length
		});
		open = false;
	}
</script>

<ResponsiveDialog.Root
	bind:open
	variant="sheet"
	title={m.new_project_secrets_title()}
	description={m.new_project_secrets_description()}
	contentClass="sm:max-w-2xl"
>
	{#snippet children()}
		<div class="grid gap-4 py-6">
			{#if sourcesQuery.isSuccess && sources.length === 0}
				<Alert.Root variant="info" icon={AlertIcon} description={m.project_secrets_no_sources()} />
			{:else}
				<div class="space-y-2">
					<Label for="new-project-secret-source">{m.project_secrets_source()}</Label>
					<Select.Root
						type="single"
						value={sourceId}
						onValueChange={(value) => {
							sourceId = value;
							keys = null;
							selection.clear();
						}}
					>
						<Select.Trigger id="new-project-secret-source" class="w-full">
							<span>{sources.find((source) => source.id === sourceId)?.name ?? m.project_secrets_select_source()}</span>
						</Select.Trigger>
						<Select.Content>
							{#each sources as source (source.id)}
								<Select.Item value={source.id}>
									<div class="flex flex-col">
										<span>{source.name}</span>
										<span class="text-xs text-muted-foreground">
											{source.provider === 'bitwarden'
												? m.secret_sources_provider_bitwarden()
												: m.secret_sources_provider_infisical()}
										</span>
									</div>
								</Select.Item>
							{/each}
						</Select.Content>
					</Select.Root>
				</div>

				{#if sourceId && provider === 'infisical'}
					{#key sourceId}
						<InfisicalTargetFields {sourceId} bind:target={infisicalTarget} />
					{/key}
				{:else if sourceId && provider === 'bitwarden'}
					{#key sourceId}
						<BitwardenTargetFields {sourceId} bind:target={bitwardenTarget} />
					{/key}
				{/if}

				{#if sourceId}
					<div class="grid gap-3 border-t border-border/50 pt-4">
						<SwitchWithLabel
							id="new-project-secret-required"
							label={m.project_secrets_required()}
							description={m.project_secrets_required_description()}
							bind:checked={required}
						/>
						<SwitchWithLabel
							id="new-project-secret-auto-redeploy"
							label={m.project_secrets_auto_redeploy()}
							description={m.project_secrets_auto_redeploy_description()}
							bind:checked={autoRedeploy}
						/>
					</div>

					<div class="flex items-center gap-2 border-t border-border/50 pt-4">
						<ArcaneButton
							action="base"
							tone="outline-primary"
							icon={RefreshIcon}
							customLabel={keys === null ? m.new_project_secrets_load() : m.new_project_secrets_reload()}
							loading={loadingKeys}
							disabled={!target || loadingKeys}
							onclick={() => void loadKeys()}
						/>
						{#if keys !== null}
							<span class="text-sm text-muted-foreground">{m.project_secrets_keys_count({ count: keys.length })}</span>
						{/if}
					</div>

					{#if keys !== null}
						{#key pickerSession}
							<ComposeRefsPicker {compose} {keys} {selection} />
						{/key}
					{/if}
				{/if}

				{#if error}
					<Alert.Root variant="destructive-subtle" icon={AlertIcon} heading={m.compose_refs_failed()} description={error} />
				{/if}
			{/if}
		</div>
	{/snippet}

	{#snippet footer()}
		<SheetFooterActions
			bind:open
			cancelDisabled={applying}
			submitAction="save"
			submitLabel={m.new_project_secrets_apply()}
			submitDisabled={!target || keys === null || applying}
			submitLoading={applying}
			onSubmit={() => void apply()}
		/>
	{/snippet}
</ResponsiveDialog.Root>
