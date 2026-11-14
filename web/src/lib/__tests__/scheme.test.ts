import { describe, expect, it } from 'vitest';
import { buildSuppliedMetadata, parseParts, setEngineTokens, trimSegment, validateScheme } from '../scheme';

const tokens = [
	{ name: 'author', value: 'author', example: 'A. Author' },
	{ name: 'title', value: 'title', example: 'The Book' },
	{ name: 'book_id', value: 'book_id', example: '12345' }
];

// The v2.2.0 capability vocabulary: plural people tokens, canonical book_id.
const pluralTokens = [
	{ name: 'title', value: 'title', example: 'The Great Gatsby' },
	{ name: 'authors', value: 'authors', example: 'F. Scott Fitzgerald' },
	{ name: 'narrators', value: 'narrators', example: 'N. Narrator' },
	{ name: 'series_name', value: 'series_name', example: 'A Series' },
	{ name: 'series_position', value: 'series_position', example: '2' },
	{ name: 'year', value: 'year', example: '1925' },
	{ name: 'genre', value: 'genre', example: 'Fiction' },
	{ name: 'book_id', value: 'book_id', example: '12345' }
];

describe('validateScheme', () => {
	it('accepts a format built only from engine tokens', () => {
		setEngineTokens(tokens);
		const { errors } = validateScheme(parseParts('{author}/{title}'));
		expect(errors).toEqual([]);
	});

	it('rejects a closed brace expression the engine does not know (E0)', () => {
		setEngineTokens(tokens);
		const { errors } = validateScheme(parseParts('{author}/{isbn}/{title}'));
		expect(errors).toEqual([
			'Path format contains unsupported token {isbn}.'
		]);
	});

	it('accepts an unclosed brace as literal text (legacy pass-through)', () => {
		setEngineTokens(tokens);
		const { errors } = validateScheme(parseParts('{title}/vol {'));
		// No E0: an unclosed brace is literal text, not a token expression.
		expect(errors).not.toContain('Path format contains unsupported token {vol}.');
	});

	it('rejects an unknown expression split across parts ({{title})', () => {
		setEngineTokens(tokens);
		// parseParts splits `{{title}` into a lone `{` text part + the {title}
		// token: on the wire it is the closed expression `{{title}`, which the
		// engine and the Go validator reject. The E0 scan must see the
		// serialized string, not per-part text.
		const parts = parseParts('{{title}');
		const { errors } = validateScheme(parts);
		// Same key the Go validator derives: the FIRST `}` closes the
		// expression, so the unknown token is `{title` (with the leading
		// brace) and the message prints `{{title}`.
		expect(errors).toEqual(['Path format contains unsupported token {{title}.']);
	});
});

describe('trimSegment', () => {
	it('trims U+0085 (NEL) where String.prototype.trim does not', () => {
		const s = 'A\u0085';
		expect(s.trim()).toBe(s); // JS keeps it
		expect(trimSegment(s)).toBe('A'); // Go strings.TrimSpace trims it
	});
});

describe('buildSuppliedMetadata', () => {
	it('sends the SuppliedMetadata shape: people/genres arrays, numeric year, no metadata_id', () => {
		const meta = buildSuppliedMetadata(pluralTokens);
		expect(meta).toEqual({
			title: 'The Great Gatsby',
			authors: ['F. Scott Fitzgerald'],
			narrators: ['N. Narrator'],
			series_name: 'A Series',
			series_position: '2',
			year: 1925,
			book_id: '12345',
			genres: ['Fiction']
		});
		expect('metadata_id' in meta).toBe(false);
	});

	it('selects plural people tokens when the capability vocabulary has them', () => {
		const meta = buildSuppliedMetadata(pluralTokens);
		expect(meta.authors).toEqual(['F. Scott Fitzgerald']);
		expect(meta.narrators).toEqual(['N. Narrator']);
	});

	it('falls back to singular people tokens only when no plural token exists, and keeps arrays present even when empty', () => {
		const meta = buildSuppliedMetadata(tokens); // only `author`, no `authors`/`narrator`
		expect(meta.authors).toEqual(['A. Author']);
		expect(meta.narrators).toEqual([]); // key present, empty: the Go field is not omitempty
	});
});
