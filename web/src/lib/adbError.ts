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
	/** True when the fix lives in the Settings page (bad path, binary, API key). */
	needsSettings?: boolean;
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

/**
 * Backend-native Go errors: the api layer writes `{"error": "<message>"}`
 * straight from os/exec/DB. Map the actionable ones to a plain sentence and
 * keep the offending path visible so the operator knows which setting to fix.
 * Returns null when the text is not a recognised backend error.
 */
function backendMessage(working: string): string | null {
	// ENOENT, in any verb-prefixed form the stdlib uses:
	//   `stat /input: no such file or directory`
	//   `cannot read src_dir /x: open /x: no such file or directory`
	// Path must not contain ':' so the leading `cannot read src_dir ...:` frame
	// is skipped and the innermost verb+path wins.
	const missing = working.match(
		/(?:stat|open|read|lstat|readdirent|chdir)\s+([^:]+):\s*(?:no such file or directory|ENOENT)/i
	);
	if (missing) {
		return `Source directory "${missing[1].trim()}" is not found on the server — check the path in Settings.`;
	}
	if (/no such file or directory|ENOENT/i.test(working)) {
		return 'A file the server needed is missing — check the configured paths in Settings.';
	}

	const denied = working.match(
		/(?:stat|open|read|lstat|readdirent|chdir)\s+([^:]+):\s*permission denied/i
	);
	if (denied) {
		return `The server can’t read "${denied[1].trim()}" — permission denied. Check that the directory is readable by the Bragi Books service.`;
	}
	if (/permission denied/i.test(working)) {
		return 'The server has no permission to read that path — check its permissions in Settings.';
	}

	// `not a directory` from the stdlib; `invalid argument` is what
	// readDirectory returns when the path exists but is a file.
	if (/not a directory/i.test(working) || /^invalid argument$/i.test(working.trim())) {
		return 'That path exists but is not a directory — pick a directory in Settings.';
	}

	if (
		/executable file not found|m4b-merge binary not found|m4b-merge start: exec|exec: .*not found/i.test(
			working
		)
	) {
		return 'The m4b-merge tool is not installed or not on the server’s PATH — set the correct binary path in Settings.';
	}

	return null;
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

	// Backend-native Go errors (os/exec) from the API's {"error": "..."} envelope.
	const backend = backendMessage(working);
	if (backend) {
		return { message: backend, rateLimited: false, needsSettings: true, raw };
	}

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

/**
 * Normalise anything a catch block can receive into a CleanedAdbError.
 * Prefers the cleaned result already attached to an ApiError (see api.ts)
 * so the original raw payload survives; falls back to cleaning the message.
 */
export function toCleanedError(e: unknown): CleanedAdbError {
	if (e && typeof e === 'object' && 'cleaned' in e) {
		const cleaned: unknown = e.cleaned;
		if (cleaned && typeof cleaned === 'object' && 'message' in cleaned && 'raw' in cleaned) {
			const { message, raw } = cleaned;
			if (typeof message === 'string' && typeof raw === 'string') {
				return {
					message,
					raw,
					rateLimited: 'rateLimited' in cleaned && cleaned.rateLimited === true,
					needsSettings: 'needsSettings' in cleaned && cleaned.needsSettings === true
				};
			}
		}
	}
	const msg =
		e instanceof Error ? e.message : typeof e === 'string' ? e : String(e ?? 'Unknown error');
	return cleanAdbError(msg);
}
