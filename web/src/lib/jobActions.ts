import type { JobAction } from './types.js';

const jobActionLabels: Record<JobAction, string> = {
	merge: 'M4B merge',
	metadata_update: 'Metadata update',
	import: 'Import',
};

export function jobActionLabel(action: string | null | undefined): string {
	// hasOwn keeps inherited Object.prototype keys (constructor, toString) out
	// of the fallback check — `in` would match them.
	if (action && Object.hasOwn(jobActionLabels, action)) {
		return jobActionLabels[action as JobAction];
	}
	return 'M4B merge';
}
