<script lang="ts">
	import ArcaneTable from '#lib/components/arcane-table/arcane-table.svelte';
	import RemoveMenuItem from '#lib/components/arcane-table/cells/remove-menu-item.svelte';
	import { UniversalMobileCard } from '#lib/components/arcane-table/index.js';
	import type { ColumnSpec, MobileFieldVisibility } from '#lib/components/arcane-table/index.js';
	import RowActionsMenu from '#lib/components/arcane-table/row-actions-menu.svelte';
	import IfPermitted from '#lib/components/if-permitted.svelte';
	import { Badge } from '#lib/components/ui/badge/index.js';
	import * as DropdownMenu from '#lib/components/ui/dropdown-menu/index.js';
	import { EditIcon, GlobeIcon, LockIcon, ClockIcon } from '#lib/icons/index.js';
	import { m } from '#lib/paraglide/messages.js';
	import type { SecretSource } from '#lib/types/secret-source.js';
	import type { Paginated, SearchPaginationSortRequest } from '#lib/types/shared.js';
	import { formatRelativeTime } from '#lib/utils/formatting.js';

	let {
		sources,
		onEdit,
		onDelete
	}: {
		sources: SecretSource[];
		onEdit: (source: SecretSource) => void;
		onDelete: (source: SecretSource) => void;
	} = $props();

	let requestOptions = $state<SearchPaginationSortRequest>({ pagination: { page: 1, limit: 20 } });
	let mobileFieldVisibility = $state<MobileFieldVisibility>({});

	const filteredSources = $derived.by(() => {
		const query = (requestOptions.search ?? '').trim().toLowerCase();
		const list = query
			? sources.filter((source) => source.name.toLowerCase().includes(query) || source.siteUrl.toLowerCase().includes(query))
			: [...sources];
		if (requestOptions.sort?.column === 'name') {
			list.sort((a, b) => a.name.localeCompare(b.name));
			if (requestOptions.sort.direction === 'desc') list.reverse();
		}
		return list;
	});

	const tableData = $derived<Paginated<SecretSource>>({
		data: filteredSources,
		pagination: {
			totalPages: 1,
			totalItems: filteredSources.length,
			currentPage: 1,
			itemsPerPage: Math.max(filteredSources.length, 1)
		}
	});

	function testLabel(source: SecretSource): string {
		if (!source.lastTestedAt) return m.secret_sources_never_tested();
		return m.secret_sources_last_tested({ time: formatRelativeTime(source.lastTestedAt) });
	}

	const columns = [
		{ accessorKey: 'name', title: m.common_name(), sortable: true, cell: NameCell },
		{ accessorKey: 'siteUrl', title: m.secret_sources_site_url(), cell: SiteCell },
		{ accessorKey: 'bindingCount', title: m.projects_title(), cell: UsageCell },
		{ accessorKey: 'lastTestedAt', title: m.common_status(), cell: StatusCell }
	] satisfies ColumnSpec<SecretSource>[];

	const mobileFields = [
		{ id: 'siteUrl', label: m.secret_sources_site_url(), defaultVisible: true },
		{ id: 'lastTestedAt', label: m.common_status(), defaultVisible: true }
	];
</script>

<ArcaneTable
	persistKey="arcane-secret-sources-table"
	items={tableData}
	bind:requestOptions
	bind:mobileFieldVisibility
	selectionDisabled={true}
	withoutPagination
	onRefresh={async () => tableData}
	{columns}
	{mobileFields}
	rowActions={RowActions}
	mobileCard={SourceMobileCard}
/>

{#snippet NameCell({ item }: { item: SecretSource })}
	<div class="flex items-center gap-2">
		<span class="font-medium">{item.name}</span>
		<Badge variant="violet" size="sm">{m.secret_sources_provider_infisical()}</Badge>
	</div>
{/snippet}

{#snippet SiteCell({ item }: { item: SecretSource })}
	<span class="max-w-70 truncate font-mono text-sm text-muted-foreground">{item.siteUrl}</span>
{/snippet}

{#snippet UsageCell({ item }: { item: SecretSource })}
	<Badge variant={item.bindingCount > 0 ? 'blue' : 'gray'} size="sm">
		{m.secret_sources_used_by({ count: item.bindingCount })}
	</Badge>
{/snippet}

{#snippet StatusCell({ item }: { item: SecretSource })}
	{#if item.lastTestError}
		<Badge variant="red" size="sm" title={item.lastTestError}>{m.secret_sources_test_failed()}</Badge>
	{:else}
		<span class="text-sm text-muted-foreground">{testLabel(item)}</span>
	{/if}
{/snippet}

{#snippet SourceMobileCard({ item, mobileFieldVisibility }: { item: SecretSource; mobileFieldVisibility: MobileFieldVisibility })}
	<UniversalMobileCard
		{item}
		icon={{ component: LockIcon, variant: 'purple' }}
		title={(item: SecretSource) => item.name}
		badges={item.lastTestError ? [{ variant: 'red' as const, text: m.secret_sources_test_failed() }] : []}
		fields={[
			{
				label: m.secret_sources_site_url(),
				getValue: (item: SecretSource) => item.siteUrl,
				icon: GlobeIcon,
				iconVariant: 'gray' as const,
				show: mobileFieldVisibility['siteUrl'] ?? true
			},
			{
				label: m.common_status(),
				getValue: (item: SecretSource) => testLabel(item),
				icon: ClockIcon,
				iconVariant: 'gray' as const,
				show: mobileFieldVisibility['lastTestedAt'] ?? true
			}
		]}
		rowActions={RowActions}
	/>
{/snippet}

{#snippet RowActions({ item }: { item: SecretSource })}
	<RowActionsMenu>
		<IfPermitted perm="secret-sources:update">
			<DropdownMenu.Item onclick={() => onEdit(item)}>
				<EditIcon class="size-4" />
				{m.common_edit()}
			</DropdownMenu.Item>
		</IfPermitted>
		<IfPermitted perm="secret-sources:delete">
			<RemoveMenuItem onclick={() => onDelete(item)} label={m.common_delete()} />
		</IfPermitted>
	</RowActionsMenu>
{/snippet}
