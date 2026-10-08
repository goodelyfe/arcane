<script lang="ts">
	import { toast } from 'svelte-sonner';

	import SecretSourceSheet from '#lib/components/sheets/secret-source-sheet.svelte';
	import * as Empty from '#lib/components/ui/empty/index.js';
	import { LockIcon } from '#lib/icons/index.js';
	import { ResourcePageLayout, type ActionButton } from '#lib/layouts/index.js';
	import { m } from '#lib/paraglide/messages.js';
	import { secretSourceService } from '#lib/services/secret-source-service.js';
	import type { SecretSource, SecretSourceCreateDto, SecretSourceUpdateDto } from '#lib/types/secret-source.js';
	import { handleApiResultWithCallbacks } from '#lib/utils/api.js';
	import { hasPermission } from '#lib/utils/auth.js';
	import { confirmAndRun } from '#lib/utils/bulk-actions.js';
	import { tryCatch } from '#lib/utils/try-catch.js';

	import SecretSourceTable from './components/secret-source-table.svelte';

	type SecretSourceFormPayload =
		| { mode: 'create'; source: SecretSourceCreateDto }
		| { mode: 'edit'; id: string; source: SecretSourceUpdateDto };

	let { data } = $props();

	let sources = $derived(data.sources);
	let isSheetOpen = $state(false);
	let sheetSession = $state(0);
	let sourceToEdit = $state<SecretSource | null>(null);
	let isSubmitting = $state(false);

	const canCreate = $derived(hasPermission('secret-sources:create'));

	function openCreateSheet() {
		sourceToEdit = null;
		sheetSession += 1;
		isSheetOpen = true;
	}

	function openEditSheet(source: SecretSource) {
		sourceToEdit = source;
		sheetSession += 1;
		isSheetOpen = true;
	}

	async function refreshSources() {
		const result = await tryCatch(secretSourceService.list());
		if (!result.error) sources = result.data;
	}

	async function handleSheetSubmit(payload: SecretSourceFormPayload) {
		const isEdit = payload.mode === 'edit';
		await handleApiResultWithCallbacks({
			result: await tryCatch(
				isEdit ? secretSourceService.update(payload.id, payload.source) : secretSourceService.create(payload.source)
			),
			message: isEdit
				? m.common_update_failed({ resource: m.secret_source() })
				: m.common_create_failed({ resource: m.secret_source() }),
			setLoadingState: (value) => (isSubmitting = value),
			onSuccess: async () => {
				toast.success(
					isEdit
						? m.common_update_success({ resource: m.secret_source() })
						: m.common_create_success({ resource: m.secret_source() })
				);
				isSheetOpen = false;
				await refreshSources();
			}
		});
	}

	function handleDelete(source: SecretSource) {
		confirmAndRun({
			title: m.common_delete_title({ resource: m.secret_source() }),
			message: m.common_delete_confirm({ resource: source.name }),
			confirmLabel: m.common_delete(),
			destructive: true,
			run: () => secretSourceService.delete(source.id),
			failureMessage: m.common_delete_failed({ resource: m.secret_source() }),
			onSuccess: async () => {
				toast.success(m.common_delete_success({ resource: m.secret_source() }));
				await refreshSources();
			}
		});
	}

	const actionButtons = $derived<ActionButton[]>(
		canCreate
			? [
					{
						id: 'create',
						action: 'create',
						placement: 'primary',
						label: m.common_add_button({ resource: m.secret_source() }),
						onclick: openCreateSheet
					}
				]
			: []
	);
</script>

<ResourcePageLayout title={m.secret_sources_title()} subtitle={m.secret_sources_subtitle()} {actionButtons}>
	{#snippet mainContent()}
		{#if sources.length === 0}
			<div class="rounded-xl border border-dashed border-border/70">
				<Empty.Root>
					<Empty.Header>
						<Empty.Media variant="icon">
							<LockIcon />
						</Empty.Media>
						<Empty.Title>{m.secret_sources_empty_title()}</Empty.Title>
						<Empty.Description>{m.secret_sources_empty_description()}</Empty.Description>
					</Empty.Header>
				</Empty.Root>
			</div>
		{:else}
			<SecretSourceTable {sources} onEdit={openEditSheet} onDelete={handleDelete} />
		{/if}
	{/snippet}

	{#snippet additionalContent()}
		{#if sheetSession > 0}
			{#key sheetSession}
				<SecretSourceSheet bind:open={isSheetOpen} {sourceToEdit} isLoading={isSubmitting} onSubmit={handleSheetSubmit} />
			{/key}
		{/if}
	{/snippet}
</ResourcePageLayout>
