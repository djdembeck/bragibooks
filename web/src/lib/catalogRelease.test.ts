import { describe, expect, it } from 'vitest';
import { catalogReleaseId, isCatalogId } from './catalogRelease';

describe('catalogReleaseId', () => {
	it('uses the release Match saved when the catalog lists it', () => {
		expect(catalogReleaseId('r2', [{ id: 'r1' }, { id: 'r2' }])).toBe('r2');
	});

	it('rejects a saved release the expanded catalog excludes', () => {
		expect(catalogReleaseId('r9', [{ id: 'r1' }, { id: 'r2' }])).toBe('');
	});

	it('keeps a saved release when membership is unknown', () => {
		expect(catalogReleaseId('r2', null)).toBe('r2');
		expect(catalogReleaseId('r2', undefined)).toBe('r2');
		expect(catalogReleaseId('r2', [])).toBe('r2');
	});

	it('rejects saved IDs that could change the request path', () => {
		for (const id of ['..', '.', '../r1', 'r1/../r2', 'r1?x=1', 'r1#frag', 'r 1', '%2e%2e']) {
			expect(catalogReleaseId(id, [{ id: 'r1' }])).toBe('');
		}
	});

	it('falls back to the first catalog release without a saved edition', () => {
		expect(catalogReleaseId('', [{ id: 'r1' }, { id: 'r2' }])).toBe('r1');
		expect(catalogReleaseId('', null)).toBe('');
		expect(catalogReleaseId('', [])).toBe('');
	});

	it('rejects an unsafe fallback release ID', () => {
		expect(catalogReleaseId('', [{ id: '..' }])).toBe('');
		expect(catalogReleaseId('', [{ id: 'r1?x=1' }])).toBe('');
	});
});

describe('isCatalogId', () => {
	it('accepts catalog-shaped IDs', () => {
		expect(isCatalogId('R8pJMp8H6Mj7')).toBe(true);
		expect(isCatalogId('release-a_2')).toBe(true);
	});

	it('rejects path-changing or empty values', () => {
		for (const id of ['', '.', '..', 'a/b', 'a?b', 'a#b', 'a b', 'a%2fb']) {
			expect(isCatalogId(id)).toBe(false);
		}
	});
});
