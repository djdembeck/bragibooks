/**
 * Shared logic for the output path format builder.
 *
 * The wire value is the raw m4b-merge `-p` template (e.g. `{author}/{title}`);
 * this module parses it into parts, validates it, and resolves a live example
 * against a pinned sample book. The builder is a view over the string —
 * serialization round-trips exactly.
 *
 * The engine substitutes `{key}` wherever it appears in the `-p` string, so
 * recognized token spellings inside literal text are treated as tokens for
 * validation and the example; the saved string is always whatever the user
 * typed.
 */

export type TokenKey =
	| 'asin'
	| 'author'
	| 'narrator'
	| 'series_name'
	| 'series_position'
	| 'subtitle'
	| 'title'
	| 'year';

export type Part =
	| { kind: 'sep' } // one "/"
	| { kind: 'token'; key: TokenKey } // "{key}"
	| { kind: 'text'; value: string }; // literal path text

export const TOKEN_ORDER: TokenKey[] = [
	'author',
	'title',
	'subtitle',
	'series_name',
	'series_position',
	'narrator',
	'year',
	'asin',
];

export const TOKEN_META: Record<TokenKey, { label: string; desc: string }> = {
	author: { label: 'Author', desc: 'First listed author' },
	narrator: { label: 'Narrator', desc: 'First listed narrator' },
	title: { label: 'Title', desc: 'Book title' },
	subtitle: { label: 'Subtitle', desc: 'Blank for most books' },
	series_name: { label: 'Series', desc: 'Series name, when known' },
	series_position: { label: 'Series position', desc: 'Numeric position in series, when known' },
	year: { label: 'Year', desc: 'Publication year' },
	asin: { label: 'ASIN (Audible ID)', desc: 'm4b-merge does not substitute this' },
};

const TOKEN_KEYS = new Set<string>(TOKEN_ORDER);

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
	for (let i = 0; i < template.length; i++) {
		const ch = template[i];
		if (ch === '{') {
			const close = template.indexOf('}', i + 1);
			if (close !== -1) {
				const key = template.slice(i + 1, close);
				if (TOKEN_KEYS.has(key)) {
					parts.push({ kind: 'token', key: key as TokenKey });
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
				if (close !== -1 && TOKEN_KEYS.has(template.slice(j + 1, close))) break;
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

function trimSegment(s: string): string {
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
 * validation and the example — the saved string is still what was typed.
 */
function splitTextPieces(value: string): TextPiece[] {
	const pieces: TextPiece[] = [];
	for (let i = 0; i < value.length; ) {
		const c = value[i];
		if (c === '{') {
			const close = value.indexOf('}', i + 1);
			if (close !== -1) {
				const key = value.slice(i + 1, close);
				if (TOKEN_KEYS.has(key)) {
					pieces.push({ token: key as TokenKey });
					i = close + 1;
					continue;
				}
			}
		}
		let j = i + 1;
		while (j < value.length) {
			if (value[j] === '{') {
				const close = value.indexOf('}', j + 1);
				if (close !== -1 && TOKEN_KEYS.has(value.slice(j + 1, close))) break;
			}
			j++;
		}
		pieces.push({ lit: value.slice(i, j) });
		i = j;
	}
	return pieces;
}

/**
 * Splits parts into "/"-separated segments: each segment accumulates the
 * literal text of its parts (token spellings stripped, other text kept
 * untrimmed) plus a flag for any recognized token, whether it is its own
 * part or typed inside text. Mirrors the engine's `/`-split.
 */
function segments(parts: Part[]) {
	const segs: { text: string; hasToken: boolean }[] = [{ text: '', hasToken: false }];
	for (const p of parts) {
		if (p.kind === 'sep') {
			segs.push({ text: '', hasToken: false });
			continue;
		}
		const cur = segs[segs.length - 1];
		if (p.kind === 'token') {
			cur.hasToken = true;
		} else {
			for (const piece of splitTextPieces(p.value)) {
				if ('token' in piece) cur.hasToken = true;
				else cur.text += piece.lit;
			}
		}
	}
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
	if (segs.every((s) => trimSegment(s.text) === '' && !s.hasToken)) {
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
		if (seg.hasToken) continue;
		const text = trimSegment(seg.text);
		if (text === '') continue;
		const base = trimSegment(text.split('.')[0]);
		if (RESERVED_NAMES.has(base.toLowerCase())) {
			errors.push(`"${base}" is a reserved device name on Windows.`);
			return { errors, warnings };
		}
	}

	// E4: at least one substitutable (non-asin) token, in any spelling.
	const hasSubstitutable = parts.some(
		(p) =>
			(p.kind === 'token' && p.key !== 'asin') ||
			(p.kind === 'text' &&
				splitTextPieces(p.value).some(
					(piece) => 'token' in piece && piece.token !== 'asin'
				))
	);
	if (!hasSubstitutable) {
		errors.push('Include at least one placeholder (e.g. {title}) so different books get different files.');
	}

	// W1: {asin} is not substituted by the engine.
	if (parts.some(
		(p) =>
			(p.kind === 'token' && p.key === 'asin') ||
			(p.kind === 'text' && splitTextPieces(p.value).some((piece) => 'token' in piece && piece.token === 'asin'))
	)) {
		warnings.push('{asin} is not substituted by m4b-merge — it would appear literally in the path. Remove it.');
	}

	// W2: every book should land in a distinct file.
	if (!parts.some(
		(p) =>
			(p.kind === 'token' && p.key === 'title') ||
			(p.kind === 'text' && splitTextPieces(p.value).some((piece) => 'token' in piece && piece.token === 'title'))
	)) {
		warnings.push('This format can produce the same file for different books — include {title}.');
	}

	return { errors, warnings };
}

/**
 * Resolve the sample book through the scheme, mirroring the engine:
 * substitute tokens (wherever they appear in the raw string), split on `/`,
 * trim, drop empty segments, join, append `.m4b`. `{asin}` is NOT
 * substituted by m4b-merge, so it stays literal. Returns `''` when nothing
 * survives (the widget shows an em dash).
 */
export function resolveExample(
	parts: Part[],
	values: Record<TokenKey, string>,
	outputDir: string
): string {
	const replaceToken = (key: TokenKey) => (key === 'asin' ? '{asin}' : values[key]);
	const raw = parts
		.map((p) => {
			if (p.kind === 'sep') return '/';
			if (p.kind === 'token') return replaceToken(p.key);
			// Text: substitute recognized token spellings, keep the rest.
			let out = '';
			for (const piece of splitTextPieces(p.value)) {
				out += 'token' in piece ? replaceToken(piece.token) : piece.lit;
			}
			return out;
		})
		.join('');
	const rel = raw
		.split('/')
		.map((s) => trimSegment(s))
		.filter((s) => s !== '')
		.join('/');
	if (!rel) return '';
	const final = rel + '.m4b';
	if (!outputDir) return final;
	return outputDir.replace(/\/+$/, '') + '/' + final;
}

/**
 * Pinned sample book for the live example: AudiobookDB release dPL9fC7YnK8X —
 * "The Martian" (2013), Andy Weir, narrated by R.C. Bray. Hard-coded so the
 * preview is deterministic and offline-safe; the ADB release page is the
 * source of truth for these values.
 */
export const MARTIAN = {
	label: '\u201cThe Martian\u201d (2013) — Andy Weir, narrated by R.C. Bray',
	link: 'https://audiobookdb.org/releases/dPL9fC7YnK8X',
	values: {
		asin: 'B00B5HZGUG',
		author: 'Andy Weir',
		narrator: 'R.C. Bray',
		series_name: '',
		series_position: '',
		subtitle: '',
		title: 'The Martian',
		year: '2013',
	} satisfies Record<TokenKey, string>,
};
