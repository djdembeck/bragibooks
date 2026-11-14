import { describe, expect, it } from 'vitest';
import { parseParts, setEngineTokens, trimSegment, validateScheme } from '../scheme';

const tokens = [
	{ name: 'author', value: 'author', example: 'A. Author' },
	{ name: 'title', value: 'title', example: 'The Book' },
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
