import type { JobAction } from './types.js';

const jobActionLabels: Record<JobAction, string> = {
	merge: 'M4B merge',
	metadata_update: 'Metadata update',
	import: 'Import',
};

export function jobActionLabel(action: string | null | undefined): string {
	if (action && action in jobActionLabels) return jobActionLabels[action as JobAction];
	return 'M4B merge';
}
