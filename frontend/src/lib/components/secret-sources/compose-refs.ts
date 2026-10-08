import type { ProjectSecretBindingDto } from '#lib/types/secret-source.js';

// A selection is a set of "service\u0000key" pairs: add KEY to that service.
const separator = '\u0000';

export function pairKey(service: string, key: string): string {
	return `${service}${separator}${key}`;
}

export function selectionToAssignments(selection: Iterable<string>): Record<string, string[]> {
	const assignments: Record<string, string[]> = {};
	for (const pair of selection) {
		const [service = '', key = ''] = pair.split(separator);
		if (!service || !key) continue;
		(assignments[service] ??= []).push(key);
	}
	return assignments;
}

// Secrets chosen while creating a project, bound once the project exists.
export type NewProjectSecrets = {
	binding: ProjectSecretBindingDto;
	sourceName: string;
	keyCount: number;
};
