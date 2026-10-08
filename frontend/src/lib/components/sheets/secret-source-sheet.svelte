<script lang="ts">
	import { untrack } from 'svelte';
	import { z } from 'zod/v4';

	import { ArcaneButton } from '#lib/components/arcane-button/index.js';
	import FormInput from '#lib/components/form/form-input.svelte';
	import SheetFooterActions from '#lib/components/sheets/sheet-footer-actions.svelte';
	import * as Alert from '#lib/components/ui/alert/index.js';
	import * as ResponsiveDialog from '#lib/components/ui/responsive-dialog/index.js';
	import { AlertIcon, CheckIcon } from '#lib/icons/index.js';
	import { m } from '#lib/paraglide/messages.js';
	import { secretSourceService } from '#lib/services/secret-source-service.js';
	import type {
		SecretSource,
		SecretSourceCreateDto,
		SecretSourceTestResult,
		SecretSourceUpdateDto
	} from '#lib/types/secret-source.js';
	import { extractApiErrorMessage } from '#lib/utils/api.js';
	import { createForm, preventDefault } from '#lib/utils/settings.svelte.js';
	import { tryCatch } from '#lib/utils/try-catch.js';

	type SecretSourceFormPayload =
		| { mode: 'create'; source: SecretSourceCreateDto }
		| { mode: 'edit'; id: string; source: SecretSourceUpdateDto };

	let {
		open = $bindable(false),
		sourceToEdit = null,
		isLoading = false,
		onSubmit
	}: {
		open: boolean;
		sourceToEdit?: SecretSource | null;
		isLoading?: boolean;
		onSubmit: (payload: SecretSourceFormPayload) => void;
	} = $props();

	const isEditMode = $derived(!!sourceToEdit);
	const hasStoredSecret = $derived(!!sourceToEdit?.hasClientSecret);

	const formSchema = untrack(() =>
		z
			.object({
				name: z.string().trim().min(1, m.common_name_required()),
				siteUrl: z.string().trim(),
				clientId: z.string().trim().min(1, m.secret_sources_client_id_required()),
				clientSecret: z.string(),
				organizationSlug: z.string().trim()
			})
			.superRefine((data, ctx) => {
				// Editing keeps the stored secret when the field is left empty.
				if (!sourceToEdit && data.clientSecret === '') {
					ctx.addIssue({ code: 'custom', path: ['clientSecret'], message: m.secret_sources_client_secret_required() });
				}
			})
	);

	const form = createForm<typeof formSchema>(
		formSchema,
		untrack(() => ({
			name: sourceToEdit?.name ?? '',
			siteUrl: sourceToEdit?.siteUrl ?? '',
			clientId: sourceToEdit?.clientId ?? '',
			clientSecret: '',
			organizationSlug: sourceToEdit?.organizationSlug ?? ''
		}))
	);
	let inputs = $derived(form.inputs);

	let testing = $state(false);
	let testResult = $state<SecretSourceTestResult | null>(null);

	async function testConnection() {
		testResult = null;
		const clientId = inputs.clientId.value.trim();
		const clientSecret = inputs.clientSecret.value;
		if (!clientId) {
			inputs.clientId.error = m.secret_sources_client_id_required();
			return;
		}
		if (!clientSecret && !hasStoredSecret) {
			inputs.clientSecret.error = m.secret_sources_client_secret_required();
			return;
		}

		testing = true;
		const result = await tryCatch(
			secretSourceService.test({
				sourceId: sourceToEdit?.id,
				siteUrl: inputs.siteUrl.value.trim(),
				clientId,
				clientSecret: clientSecret || undefined,
				organizationSlug: inputs.organizationSlug.value.trim()
			})
		);
		testing = false;
		testResult = result.error
			? { ok: false, message: extractApiErrorMessage(result.error), projectsVisible: 0, canListProjects: false }
			: result.data;
	}

	function handleSubmit() {
		const data = form.validate();
		if (!data) return;

		if (isEditMode && sourceToEdit) {
			const dto: SecretSourceUpdateDto = {
				name: data.name,
				siteUrl: data.siteUrl,
				clientId: data.clientId,
				organizationSlug: data.organizationSlug
			};
			if (data.clientSecret) dto.clientSecret = data.clientSecret;
			onSubmit({ mode: 'edit', id: sourceToEdit.id, source: dto });
			return;
		}

		onSubmit({
			mode: 'create',
			source: {
				name: data.name,
				siteUrl: data.siteUrl || undefined,
				clientId: data.clientId,
				clientSecret: data.clientSecret,
				organizationSlug: data.organizationSlug || undefined
			}
		});
	}
</script>

<ResponsiveDialog.Root
	bind:open
	variant="sheet"
	title={isEditMode ? m.secret_sources_edit_title() : m.secret_sources_add_title()}
	description={isEditMode ? m.common_edit_description() : m.secret_sources_add_description()}
	contentClass="sm:max-w-md"
>
	{#snippet children()}
		<form id="secret-source-form" onsubmit={preventDefault(handleSubmit)} class="grid gap-4 py-6">
			<FormInput label={m.common_name()} type="text" placeholder={m.secret_sources_name_placeholder()} bind:input={inputs.name} />

			<FormInput
				label={m.secret_sources_site_url()}
				type="text"
				placeholder="https://app.infisical.com"
				helpText={m.secret_sources_site_url_description()}
				bind:input={inputs.siteUrl}
			/>

			<FormInput label={m.secret_sources_client_id()} type="text" autocomplete="off" bind:input={inputs.clientId} />

			<FormInput
				label={m.secret_sources_client_secret()}
				type="password"
				autocomplete="new-password"
				placeholder={hasStoredSecret ? m.common_keep_placeholder() : ''}
				bind:input={inputs.clientSecret}
			/>

			<FormInput
				label={m.secret_sources_organization_slug()}
				type="text"
				helpText={m.secret_sources_organization_slug_description()}
				bind:input={inputs.organizationSlug}
			/>

			<div class="space-y-3 border-t border-border/50 pt-4">
				<ArcaneButton
					action="test"
					tone="outline"
					type="button"
					customLabel={m.test_connection()}
					loading={testing}
					disabled={testing || isLoading}
					onclick={testConnection}
				/>

				{#if testResult}
					{#if testResult.ok}
						<Alert.Root
							variant="primary-subtle"
							icon={CheckIcon}
							heading={testResult.canListProjects
								? m.secret_sources_test_success({ count: testResult.projectsVisible })
								: testResult.message}
						/>
					{:else}
						<Alert.Root
							variant="destructive-subtle"
							icon={AlertIcon}
							heading={m.secret_sources_test_failed()}
							description={testResult.message}
						/>
					{/if}
				{/if}
			</div>
		</form>
	{/snippet}

	{#snippet footer()}
		<SheetFooterActions
			bind:open
			cancelDisabled={isLoading}
			submitAction={isEditMode ? 'save' : 'create'}
			submitForm="secret-source-form"
			submitDisabled={isLoading || testing}
			submitLoading={isLoading}
			submitLabel={isEditMode ? m.common_save_changes() : m.common_add_button({ resource: m.secret_source() })}
		/>
	{/snippet}
</ResponsiveDialog.Root>
