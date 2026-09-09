/**
 * Central cleaner for errors surfaced from the AudiobookDB pipeline.
 *
 * Raw messages look like:
 *   `API 502: audiobookdb get book: audiobookdb 429: {"errors":[{...}]}`
 * — a frontend prefix, the Go proxy's context, the upstream status, and
 * a JSON:API body. Operators should never see the JSON.
 */

export interface CleanedAdbError {
	/** One human sentence; never JSON. */
	message: string;
	/** True when the upstream limit is the ADB rate limit (429). */
	rateLimited: boolean;
	/** The original message, for "Technical detail" expandables. */
	raw: string;
}

const JSON_BODY_RE = /\{[\s\S]*\}/;

function extractJson(text: string): Record<string, unknown> | null {
	const m = text.match(JSON_BODY_RE);
	if (!m) return null;
	try {
		const parsed = JSON.parse(m[0]) as Record<string, unknown>;
		return parsed && typeof parsed === 'object' ? parsed : null;
	} catch {
		return null;
	}
}

/** Walk a JSON:API errors[] body to its most human entry. */
function jsonApiMessage(body: Record<string, unknown>): string | null {
	const errors = body.errors;
	if (!Array.isArray(errors) || errors.length === 0) return null;
	const first = errors[0] as Record<string, unknown>;
	const detail = typeof first.detail === 'string' ? first.detail : '';
	const title = typeof first.title === 'string' ? first.title : '';
	return detail.trim() || title.trim() || null;
}

export function cleanAdbError(message: string): CleanedAdbError {
	const raw = message;
	let working = message;

	const rateLimited =
		/429|rate limit/i.test(working) || ((): boolean => {
			const body = extractJson(working);
			return body ? /429|rate limit/i.test(JSON.stringify(body)) : false;
		})();

	if (rateLimited) {
		return {
			message:
				'AudiobookDB rate limit reached — wait a minute and retry, or add an API key in Settings for higher limits (free account).',
			rateLimited: true,
			raw
		};
	}

	// Nested bodies: the proxy wraps upstream text in {"error": "<text>"},
	// and that text may itself carry a JSON:API body.
	for (let i = 0; i < 3; i++) {
		const body = extractJson(working);
		if (!body) break;
		const api = jsonApiMessage(body);
		if (api) {
			working = api;
			continue;
		}
		const inner = body.error ?? body.detail ?? body.message;
		if (typeof inner === 'string' && inner.trim()) {
			working = inner;
			continue;
		}
		break;
	}

	// Strip frontend/proxy prefixes: `API 502: `, `audiobookdb ...: `
	working = working.replace(/^API \d{3}:\s*/, '');
	working = working.replace(/^audiobookdb [^:]+:\s*/, '');

	if (/cannot unmarshal|json: /i.test(working)) {
		return {
			message: 'AudiobookDB returned a record the pipeline couldn’t read — try Custom search below.',
			rateLimited: false,
			raw
		};
	}

	// Anything still JSON-looking is a failure of the walker — say so plainly.
	if (working.includes('{') && working.includes('"')) {
		return {
			message: 'AudiobookDB returned an unexpected error — retry, or check Status in Settings.',
			rateLimited: false,
			raw
		};
	}

	if (working.length > 200) working = `${working.slice(0, 200)}…`;
	return { message: working, rateLimited: false, raw };
}
