/**
 * Shared logic for the output path format builder.
 *
 * The wire value is the raw path template (e.g. `{author}/{title}`);
 * this module parses it into parts, validates it, and reports the live
 * example. The builder is a view over the string — serialization
 * round-trips exactly.
 *
 * The token vocabulary is NOT owned here: the ENGINE's verified
 * --capabilities document is the single source of truth, installed into
 * `engineTokens` (via `setEngineTokens`) when the app loads
 * /api/engine/capabilities. Validation mirrors the server's
 * capability-driven rules; the live name example comes from the server's
 * non-mutating job preview, not a local sample-book resolver.
 */

import { get, writable } from 'svelte/store';

export interface EngineToken {
	name: string;
	value: string;
	example: string;
}

/**
 * Reactive store of the engine's verified path tokens, in the engine's
 * declared order (the Settings palette renders it as-is). Empty until
 * /api/engine/capabilities loads; components must handle the empty state
 * (the app refuses to save a format without a verified engine anyway).
 */
export const engineTokens = writable<EngineToken[]>([]);
export function setEngineTokens(tokens: EngineToken[]): void {
	engineTokens.set(tokens);
}

export type TokenKey = string;

export type Part =
	| { kind: 'sep' } // one "/"
	| { kind: 'token'; key: TokenKey } // "{key}"
	| { kind: 'text'; value: string }; // literal path text

// Windows-forbidden set; applied on every OS because output roots are
// commonly SMB/NFS mounts owned by a foreign OS. Tuple, not a string, so
// .includes(ch) is unambiguous (spaces are legal in paths).
export const ILLEGAL_CHARS = ['<', '>', ':', '"', '\\', '|', '?', '*'] as const;

const RESERVED_NAMES = new Set([
	'con',
	'prn',
	'aux',
	'nul',
	...Array.from({ length: 9 }, (_, i) => `com${i + 1}`),
	...Array.from({ length: 9 }, (_, i) => `lpt${i + 1}`),
]);

/**
 * Parse a template string into parts, left to right.
 * `{key}` with a known key → token; `/` → sep; everything else (stray
 * braces, spaces, dots, backslashes) merges into text runs. Never throws —
 * legacy weirdness becomes text.
 */
export function parseParts(template: string): Part[] {
	const parts: Part[] = [];
	const keys = new Set(get(engineTokens).map((t) => t.name));
	for (let i = 0; i < template.length; i++) {
		const ch = template[i];
		if (ch === '{') {
			const close = template.indexOf('}', i + 1);
			if (close !== -1) {
				const key = template.slice(i + 1, close);
				if (keys.has(key)) {
					parts.push({ kind: 'token', key });
					i = close;
					continue;
				}
			}
		}
		if (ch === '/') {
			parts.push({ kind: 'sep' });
			continue;
		}
		// Text run: consume until the next token or separator.
		let j = i + 1;
		while (j < template.length) {
			const c = template[j];
			if (c === '/') break;
			if (c === '{') {
				const close = template.indexOf('}', j + 1);
				if (close !== -1 && keys.has(template.slice(j + 1, close))) break;
			}
			j++;
		}
		parts.push({ kind: 'text', value: template.slice(i, j) });
		i = j - 1;
	}
	return parts;
}

/** Serialize parts back to the raw template string. */
export function serializeParts(parts: Part[]): string {
	return parts
		.map((p) =>
			p.kind === 'sep' ? '/' : p.kind === 'token' ? `{${p.key}}` : p.value
		)
		.join('');
}

function isControl(ch: string): boolean {
	const cp = ch.codePointAt(0)!;
	return cp < 0x20 || cp === 0x7f;
}

// Whitespace the Go validator (unicode.IsSpace) treats as trimmable:
// tab/LF/VT/FF/CR/space, U+0085 NEL, and the remaining White_Space set.
// Notably NOT all C0 controls, and NOT U+FEFF — both differ from
// String.prototype.trim, which is why this set is explicit.
function isGoSpace(ch: string): boolean {
	const cp = ch.codePointAt(0)!;
	return (
		(cp >= 0x09 && cp <= 0x0d) ||
		cp === 0x20 ||
		cp === 0x85 ||
		cp === 0xa0 ||
		cp === 0x1680 ||
		(cp >= 0x2000 && cp <= 0x200a) ||
		cp === 0x2028 ||
		cp === 0x2029 ||
		cp === 0x202f ||
		cp === 0x205f ||
		cp === 0x3000
	);
}

/**
 * Trim a path segment with Go's strings.TrimSpace semantics (unicode.IsSpace:
 * tab/LF/VT/FF/CR/space, U+0085, U+00A0, U+1680, U+2000–U+200A, U+2028,
 * U+2029, U+202F, U+205F, U+3000). Exported so callers that mirror the Go
 * validator's segment rules elsewhere (e.g. the settings page's ".." parity
 * check) agree with the server — String.prototype.trim accepts more, so it
 * must not be used there.
 */
export function trimSegment(s: string): string {
	let a = 0;
	let b = s.length;
	while (a < b && isGoSpace(s[a])) a++;
	while (b > a && isGoSpace(s[b - 1])) b--;
	return s.slice(a, b);
}

type TextPiece = { lit: string } | { token: TokenKey };

/**
 * Splits a literal text run into literal pieces and recognized token
 * spellings. The engine substitutes `{key}` wherever it appears in the
 * template, so `{title}` typed inside a text part behaves like a token for
 * validation — the saved string is still what was typed.
 */
function splitTextPieces(value: string): TextPiece[] {
	const pieces: TextPiece[] = [];
	const keys = new Set(get(engineTokens).map((t) => t.name));
	for (let i = 0; i < value.length; ) {
		const c = value[i];
		if (c === '{') {
			const close = value.indexOf('}', i + 1);
			if (close !== -1) {
				const key = value.slice(i + 1, close);
				if (keys.has(key)) {
					pieces.push({ token: key });
					i = close + 1;
					continue;
				}
			}
		}
		let j = i + 1;
		while (j < value.length) {
			if (value[j] === '{') {
				const close = value.indexOf('}', j + 1);
				if (close !== -1 && keys.has(value.slice(j + 1, close))) break;
			}
			j++;
		}
		pieces.push({ lit: value.slice(i, j) });
		i = j;
	}
	return pieces;
}

type Segment = { text: string; tokens: TokenKey[] };

/**
 * Splits parts into "/"-separated segments: each segment keeps its literal
 * text (token spellings stripped, untrimmed) plus the recognized token keys
 * in order of appearance, whether a token is its own part or typed inside
 * text. Adjacent text parts are concatenated before the token scan — on the
 * wire they are one run, so a spelling split across two parts (e.g. `{tit`
 * + `le}`) is recognized as a token. Mirrors the engine's `/`-split.
 */
function segments(parts: Part[]): Segment[] {
	const segs: Segment[] = [{ text: '', tokens: [] }];
	let buffered = '';
	const flush = () => {
		if (buffered === '') return;
		const cur = segs[segs.length - 1];
		for (const piece of splitTextPieces(buffered)) {
			if ('token' in piece) cur.tokens.push(piece.token);
			else cur.text += piece.lit;
		}
		buffered = '';
	};
	for (const p of parts) {
		if (p.kind === 'sep') {
			flush();
			segs.push({ text: '', tokens: [] });
			continue;
		}
		if (p.kind === 'token') {
			flush();
			segs[segs.length - 1].tokens.push(p.key);
		} else {
			buffered += p.value;
		}
	}
	flush();
	return segs;
}

/**
 * Validate a scheme. `errors` block saving (the Go validator mirrors these);
 * `warnings` are advisory UI hints only.
 */
export function validateScheme(parts: Part[]): { errors: string[]; warnings: string[] } {
	const errors: string[] = [];
	const warnings: string[] = [];
	const segs = segments(parts);

	// E1: zero path elements after trimming.
	if (segs.every((s) => trimSegment(s.text) === '' && s.tokens.length === 0)) {
		errors.push('Add at least one path element.');
	}

	// E2: forbidden characters in literal text (only the literal pieces —
	// the braces of a recognized token are fine). One error naming the first
	// offender.
	for (const p of parts) {
		if (p.kind !== 'text') continue;
		for (const piece of splitTextPieces(p.value)) {
			if ('token' in piece) continue;
			for (const ch of piece.lit) {
				if (isControl(ch)) {
					errors.push(`Path text cannot contain 0x${ch.codePointAt(0)!.toString(16)}`);
					return { errors, warnings };
				}
				if ((ILLEGAL_CHARS as readonly string[]).includes(ch)) {
					errors.push(`Path text cannot contain "${ch}"`);
					return { errors, warnings };
				}
			}
		}
	}

	// E3: reserved Windows device names as a whole static segment (base
	// name before the first '.'). Adjacent text parts form one segment —
	// the engine concatenates them before checking.
	for (const seg of segs) {
		if (seg.tokens.length > 0) continue;
		const text = trimSegment(seg.text);
		if (text === '') continue;
		const base = trimSegment(text.split('.')[0]);
		if (RESERVED_NAMES.has(base.toLowerCase())) {
			errors.push(`"${base}" is a reserved device name on Windows.`);
			return { errors, warnings };
		}
	}

	// E4: at least one placeholder from the engine's token set (a token part
	// or a recognized token spelling typed inside text).
	const keys = new Set(get(engineTokens).map((t) => t.name));
	let hasToken = parts.some((p) => p.kind === 'token' && keys.has(p.key));
	if (!hasToken) {
		for (const p of parts) {
			if (p.kind !== 'text') continue;
			for (const piece of splitTextPieces(p.value)) {
				if ('token' in piece && keys.has(piece.token)) {
					hasToken = true;
					break;
				}
			}
			if (hasToken) break;
		}
	}
	if (!hasToken) {
		errors.push('Add at least one placeholder such as {title}.');
	}

	return { errors, warnings };
}
