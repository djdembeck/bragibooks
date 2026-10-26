import type { JobAction } from './types.js';

const jobActionLabels: Record<JobAction, string> = {
	merge: 'M4B merge',
	metadata_update: 'Metadata update',
	import: 'Import',
};

export function jobActionLabel(action: string | null | undefined): string {
	// A missing action predates the column or the payload field; those rows
	// were merges. Anything else unrecognized is labeled honestly rather than
	// guessed as a merge.
	if (!action) {
		return jobActionLabels.merge;
	}
	// hasOwn keeps inherited Object.prototype keys (constructor, toString) out
	// of the lookup — `in` would match them.
	if (Object.hasOwn(jobActionLabels, action)) {
		return jobActionLabels[action as JobAction];
	}
	return 'Unknown action';
}
