import { describe, expect, it } from 'vitest';
import { publisherName } from './publisher';

describe('publisherName', () => {
	it('returns undefined without a release or publisher credits', () => {
		expect(publisherName(null)).toBeUndefined();
		expect(publisherName(undefined)).toBeUndefined();
		expect(publisherName({ publishers: [] })).toBeUndefined();
	});

	it('reads the publishers array and skips blank names', () => {
		expect(
			publisherName({
				publishers: [
					{ id: 'p1', name: '  ' },
					{ id: 'p2', name: ' Podium Audio ' }
				]
			})
		).toBe('Podium Audio');
	});

	it('joins multiple credits in upstream order', () => {
		expect(
			publisherName({
				publishers: [
					{ id: 'p1', name: 'Podium Audio' },
					{ id: 'p2', name: 'Second Press' }
				]
			})
		).toBe('Podium Audio, Second Press');
	});
});
