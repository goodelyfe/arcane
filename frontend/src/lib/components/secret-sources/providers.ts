import { m } from '#lib/paraglide/messages.js';
import type { BindingTarget, SecretProvider } from '#lib/types/secret-source.js';

// Provider metadata shared by the source sheet, the binding forms, and the
// summaries. Infisical and Bitwarden keep their own target components; the
// providers below use ProviderTargetFields.

export const providerOptions: { value: SecretProvider; label: () => string; description: () => string }[] = [
	{
		value: 'infisical',
		label: m.secret_sources_provider_infisical,
		description: m.secret_sources_provider_infisical_description
	},
	{
		value: 'bitwarden',
		label: m.secret_sources_provider_bitwarden,
		description: m.secret_sources_provider_bitwarden_description
	},
	{ value: 'vault', label: m.secret_sources_provider_vault, description: m.secret_sources_provider_vault_description },
	{ value: 'doppler', label: m.secret_sources_provider_doppler, description: m.secret_sources_provider_doppler_description },
	{
		value: 'onepassword',
		label: m.secret_sources_provider_onepassword,
		description: m.secret_sources_provider_onepassword_description
	},
	{ value: 'http', label: m.secret_sources_provider_http, description: m.secret_sources_provider_http_description }
];

export type GenericProvider = 'vault' | 'doppler' | 'onepassword' | 'http';

export function isGenericProvider(provider: SecretProvider | undefined): provider is GenericProvider {
	return provider === 'vault' || provider === 'doppler' || provider === 'onepassword' || provider === 'http';
}

export function providerLabel(provider: SecretProvider | undefined): string {
	return (providerOptions.find((option) => option.value === provider) ?? providerOptions[0]!).label();
}

// Providers whose guided setup can write secrets.
export function providerCanSetUp(provider: SecretProvider): boolean {
	return provider === 'infisical' || provider === 'bitwarden' || provider === 'vault';
}

// Fills in the target of a generic provider so its fields have values to bind.
export function withDefaultTarget(provider: GenericProvider, target: BindingTarget): BindingTarget {
	switch (provider) {
		case 'vault':
			return { vault: target.vault ?? { mount: 'secret', path: '', kvVersion: 2 } };
		case 'doppler':
			return { doppler: target.doppler ?? { project: '', config: '' } };
		case 'onepassword':
			return { onepassword: target.onepassword ?? { scope: 'vault', vaultId: '', itemId: '', name: '' } };
		case 'http':
			return { http: target.http ?? { path: '' } };
	}
}

export function isGenericTargetComplete(provider: GenericProvider, target: BindingTarget): boolean {
	switch (provider) {
		case 'vault': {
			// A path ending in "/" is a folder, not a secret.
			const path = target.vault?.path.trim() ?? '';
			return !!path && !path.endsWith('/');
		}
		case 'doppler':
			// Both empty (service token) or both set.
			return !!target.doppler && !!target.doppler.project?.trim() === !!target.doppler.config?.trim();
		case 'onepassword': {
			const op = target.onepassword;
			return !!op?.vaultId && (op.scope === 'vault' || !!op.itemId);
		}
		case 'http':
			return !!target.http;
	}
}

// Keeps only the selected provider's target, as the API expects.
export function pickGenericTarget(provider: GenericProvider, target: BindingTarget): BindingTarget {
	return withDefaultTarget(provider, target);
}

export function describeGenericTarget(target: BindingTarget): string {
	if (target.vault) {
		return `${target.vault.mount}/${target.vault.path} · KV v${target.vault.kvVersion}`;
	}
	if (target.doppler) {
		return target.doppler.project ? `${target.doppler.project} · ${target.doppler.config}` : m.doppler_service_token_config();
	}
	if (target.onepassword) {
		const scope = target.onepassword.scope === 'item' ? m.onepassword_scope_item() : m.onepassword_scope_vault();
		return `${scope} · ${target.onepassword.name || target.onepassword.itemId || target.onepassword.vaultId}`;
	}
	if (target.http) {
		return target.http.path ? `/${target.http.path}` : m.http_target_root();
	}
	return '';
}
