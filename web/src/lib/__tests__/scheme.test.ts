import { describe, expect, it } from 'vitest';
import { parseParts, setEngineTokens, validateScheme } from '../scheme';

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
});
