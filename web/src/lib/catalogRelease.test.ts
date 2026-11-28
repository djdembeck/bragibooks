import { describe, expect, it } from 'vitest';
import { catalogReleaseId } from './catalogRelease';

describe('catalogReleaseId', () => {
	it('uses the release Match saved', () => {
		expect(catalogReleaseId('r2', [{ id: 'r1' }, { id: 'r2' }])).toBe('r2');
	});

	it('falls back to the first catalog release without a saved edition', () => {
		expect(catalogReleaseId('', [{ id: 'r1' }, { id: 'r2' }])).toBe('r1');
		expect(catalogReleaseId('', null)).toBe('');
	});

	it('returns empty when neither source has an edition', () => {
		expect(catalogReleaseId('', [])).toBe('');
		expect(catalogReleaseId('', undefined)).toBe('');
	});
});
