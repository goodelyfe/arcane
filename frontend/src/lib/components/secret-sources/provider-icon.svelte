<script lang="ts">
	import type { Component } from 'svelte';
	import OnePasswordIcon from 'virtual:icons/selfhst/1password';
	// Brand marks from the selfh.st icon set (CC BY 4.0), the same set Arcane
	// offers as an icon catalog. They are bundled, so no request leaves Arcane.
	import BitwardenIcon from 'virtual:icons/selfhst/bitwarden';
	import DopplerIcon from 'virtual:icons/selfhst/doppler';
	import VaultIcon from 'virtual:icons/selfhst/hashicorp-vault';
	import InfisicalIcon from 'virtual:icons/selfhst/infisical';

	import { ConnectionIcon } from '#lib/icons/index.js';
	import type { SecretProvider } from '#lib/types/secret-source.js';
	import { cn } from '#lib/utils.js';

	let { provider, class: className }: { provider: SecretProvider | undefined; class?: string } = $props();

	const brandIcons: Partial<Record<SecretProvider, Component>> = {
		bitwarden: BitwardenIcon,
		doppler: DopplerIcon,
		infisical: InfisicalIcon,
		onepassword: OnePasswordIcon,
		vault: VaultIcon
	};

	const Brand = $derived(provider ? brandIcons[provider] : undefined);
</script>

<!-- Brand colors are made for light backgrounds, so brand marks sit on a white tile in both themes. -->
<span
	class={cn(
		'inline-flex size-6 shrink-0 items-center justify-center rounded-md p-0.5 ring-1 ring-border/60',
		Brand ? 'bg-white' : 'bg-muted text-muted-foreground',
		className
	)}
	aria-hidden="true"
>
	{#if Brand}
		<Brand class="size-full" />
	{:else}
		<ConnectionIcon class="size-full" />
	{/if}
</span>
