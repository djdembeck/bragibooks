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

	it('still reports a write failure when the child names no path', () => {
		const cleaned = cleanAdbError(M4B_MERGE_NOT_WRITABLE);
		expect(cleaned.message).toBe(
			'The server can’t write to a configured directory — permission denied. Check that the directory is writable by the Bragi Books service.'
		);
		expect(cleaned.message).not.toContain('read');
	});

	it('keeps a not-a-directory cause instead of blaming permissions', () => {
		const cleaned = cleanAdbError(
			'completed directory "/erotica/books/file" is not writable: /erotica/books/file is not a directory'
		);
		expect(cleaned.message).toBe(
			'That path exists but is not a directory — pick a directory in Settings.'
		);
	});

	it('keeps a read-only mount cause', () => {
		const cleaned = cleanAdbError(
			'output directory "/media/books" is not writable: read-only file system'
		);
		expect(cleaned.message).toBe(
			'The server can’t write to "/media/books" — the file system is read-only. Remount it writable or pick another directory in Settings.'
		);
	});

	it('keeps a full-disk cause', () => {
		const cleaned = cleanAdbError(
			'completed directory "/media/books" is not writable: no space left on device'
		);
		expect(cleaned.message).toBe(
			'The server can’t write to "/media/books" — the disk is full. Free space or pick another directory in Settings.'
		);
	});

	it('passes any other write-cause detail through', () => {
		const cleaned = cleanAdbError(
			'output directory "/mnt/nfs" is not writable: input/output error'
		);
		expect(cleaned.message).toBe(
			'The server can’t write to "/mnt/nfs" — input/output error. Check the output and completed directory paths in Settings.'
		);
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
