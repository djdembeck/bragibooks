import { describe, expect, it } from 'vitest';
import { cleanAdbError } from './adbError';

// The job error the API persists when m4b-merge's startup validation fails.
const M4B_MERGE_NOT_WRITABLE =
	'm4b-merge: exit status 1: Error: Completed directory is not writable: Permission denied (os error 13)\n';

describe('cleanAdbError write-permission mapping', () => {
	it('names the completed directory the API probed', () => {
		const cleaned = cleanAdbError(
			'm4b-merge: exit status 1: completed directory "/erotica/completed" is not writable: permission denied'
		);
		expect(cleaned.message).toBe(
			'The server can’t write to "/erotica/completed" — permission denied. Check that the directory is writable by the Bragi Books service.'
		);
		expect(cleaned.needsSettings).toBe(true);
		expect(cleaned.rateLimited).toBe(false);
	});

	it('reports the innermost Go verb+path for a create failure', () => {
		const cleaned = cleanAdbError(
			'create job output dir /erotica/books/job1.tmp: mkdir /erotica/books/job1.tmp: permission denied'
		);
		expect(cleaned.message).toBe(
			'The server can’t write to "/erotica/books/job1.tmp" — permission denied. Check that the directory is writable by the Bragi Books service.'
		);
		expect(cleaned.needsSettings).toBe(true);
	});

	it('still falls back to a write message when the child names no path', () => {
		const cleaned = cleanAdbError(M4B_MERGE_NOT_WRITABLE);
		expect(cleaned.message).toBe(
			'The server can’t write to a configured directory — check the output and completed directory permissions in Settings.'
		);
		expect(cleaned.message).not.toContain('read');
	});

	it('keeps read denials read-oriented', () => {
		const cleaned = cleanAdbError('cannot read src_dir /input/x: open /input/x: permission denied');
		expect(cleaned.message).toBe(
			'The server can’t read "/input/x" — permission denied. Check that the directory is readable by the Bragi Books service.'
		);
	});

	it('does not blame a directory when the denial names no verb', () => {
		const cleaned = cleanAdbError('the server answered: permission denied');
		expect(cleaned.message).toBe(
			'The server has no permission to access that path — check its permissions in Settings.'
		);
	});
});
