/**
 * Shared logic for the output path format builder.
 *
 * The wire value is the raw m4b-merge `-p` template (e.g. `{author}/{title}`);
 * this module parses it into parts, validates it, and resolves a live example
 * against a pinned sample book. The builder is a view over the string —
 * serialization round-trips exactly.
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

/**
 * Segments of the path: consecutive parts between separators, empty text
 * parts dropped, text trimmed. Mirrors the engine's `/`-split semantics.
 */
function segments(parts: Part[]): { text: string; hasToken: boolean }[][] {
	const segs: { text: string; hasToken: boolean }[][] = [[], ];
	for (const p of parts) {
		if (p.kind === 'sep') {
			segs.push([]);
		} else if (p.kind === 'token') {
			segs[segs.length - 1].push({ text: '', hasToken: true });
		} else if (p.value.trim() !== '') {
			segs[segs.length - 1].push({ text: p.value.trim(), hasToken: false });
		}
	}
	return segs;
}

function isControl(ch: string): boolean {
	const cp = ch.codePointAt(0)!;
	return cp < 0x20 || cp === 0x7f;
}

/**
 * Validate a scheme. `errors` block saving (the Go validator mirrors these);
 * `warnings` are advisory UI hints only.
 */
export function validateScheme(parts: Part[]): { errors: string[]; warnings: string[] } {
	const errors: string[] = [];
	const warnings: string[] = [];

	// E1: zero path elements after trimming.
	if (segments(parts).every((s) => s.length === 0)) {
		errors.push('Add at least one path element.');
	}

	// E2: forbidden characters in text ( '\\' < > : " \\ | ? * and controls).
	// One error naming the first offender.
	for (const p of parts) {
		if (p.kind !== 'text') continue;
		for (const ch of p.value) {
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

	// E3: reserved Windows device names as a whole segment (base name).
	for (const seg of segments(parts)) {
		if (seg.length !== 1) continue;
		const [only] = seg;
		if (only.hasToken) continue;
		const base = only.text.split('.')[0].toLowerCase();
		if (RESERVED_NAMES.has(base)) {
			errors.push(`"${only.text.split('.')[0]}" is a reserved device name on Windows.`);
			return { errors, warnings };
		}
	}

	// E4: at least one substitutable (non-asin) token.
	const hasSubstitutable = parts.some((p) => p.kind === 'token' && p.key !== 'asin');
	if (!hasSubstitutable) {
		errors.push('Include at least one placeholder (e.g. {title}) so different books get different files.');
	}

	// W1: {asin} is not substituted by the engine.
	if (parts.some((p) => p.kind === 'token' && p.key === 'asin')) {
		warnings.push('{asin} is not substituted by m4b-merge — it would appear literally in the path. Remove it.');
	}

	// W2: every book should land in a distinct file.
	if (!parts.some((p) => p.kind === 'token' && p.key === 'title')) {
		warnings.push('This format can produce the same file for different books — include {title}.');
	}

	return { errors, warnings };
}

/**
 * Resolve the sample book through the scheme, mirroring the engine:
 * substitute tokens, split on `/`, trim, drop empty segments, join, append
 * `.m4b`. `{asin}` is NOT substituted by m4b-merge, so it stays literal.
 * Returns `''` when nothing survives (the widget shows an em dash).
 */
export function resolveExample(
	parts: Part[],
	values: Record<TokenKey, string>,
	outputDir: string
): string {
	const raw = parts
		.map((p) => (p.kind === 'sep' ? '/' : p.kind === 'token' ? (p.key === 'asin' ? '{asin}' : values[p.key]) : p.value))
		.join('');
	const rel = raw
		.split('/')
		.map((s) => s.trim())
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
