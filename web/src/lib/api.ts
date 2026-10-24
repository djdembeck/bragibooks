import { cleanAdbError, type CleanedAdbError } from '$lib/adbError';

const API_BASE = '';

/**
 * Error thrown by every api.ts call. `message` is the operator sentence;
 * `cleaned` carries the full result (including `raw`) so pages can offer a
 * "Technical detail" expander. Subclasses Error so `instanceof Error` holds.
 */
export class ApiError extends Error {
	readonly status: number;
	readonly cleaned: CleanedAdbError;

	constructor(status: number, text: string) {
		const cleaned = cleanAdbError(`API ${status}: ${text}`);
		super(cleaned.message);
		this.name = 'ApiError';
		this.status = status;
		this.cleaned = cleaned;
	}
}

// Optional admin token (set when the server runs with BRAGIBOOKS_ADMIN_TOKEN);
// absent (no header) when the token is not stored.
function authHeaders(): Record<string, string> {
	try {
		const token = window.localStorage.getItem('bragibooks_token');
		if (token) {
			// EventSource cannot send headers; mirror the token into a cookie so
			// SSE streams authenticate against the server-side cookie check.
			// No Secure flag: local HTTP must work. The cookie persists after
			// log-out, which is acceptable for a local-first admin tool.
			// Written verbatim (the server percent-decodes before comparing), so
			// base64 chars like +/= match; cookie-breaking tokens are skipped
			// and SSE degrades to polling while fetches keep using the header.
			if (!/[;\s]/.test(token)) document.cookie = `bragibooks_token=${token}; path=/`;
		}
		const headers: Record<string, string> = {};
		if (token) headers.Authorization = `Bearer ${token}`;
		// The server requires a double-submit CSRF token on state-changing
		// requests when no Authorization header is present (local mode).
		// Echo the server-set bragibooks_csrf cookie into X-CSRF-Token;
		// request with no cookie (first hit) sends an empty value, which
		// the server rejects and the next UI action retries after the
		// GET (or any prior response) has set the cookie.
		const csrf = document.cookie
			.split('; ')
			.find((c) => c.startsWith('bragibooks_csrf='))
			?.slice('bragibooks_csrf='.length);
		if (csrf && !headers.Authorization) {
			headers['X-CSRF-Token'] = csrf;
		}
		return headers;
	} catch {
		return {};
	}
}

export async function get<T>(path: string, init?: RequestInit): Promise<T> {
	const res = await fetch(`${API_BASE}/api${path}`, {
		...init,
		headers: { Accept: 'application/json', ...authHeaders(), ...(init?.headers ?? {}) }
	});
	if (!res.ok) {
		const text = await res.text();
		throw new ApiError(res.status, text);
	}
	if (res.status === 204) return undefined as T;
	return (await res.json()) as T;
}

export async function post<T>(path: string, body: unknown, init?: RequestInit): Promise<T> {
	return get(path, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
		body: JSON.stringify(body),
		...init
	});
}

export async function put<T>(path: string, body: unknown): Promise<T> {
	return post(path, body, { method: 'PUT' });
}

export async function del<T>(path: string): Promise<T> {
	return get(path, { method: 'DELETE' });
}

export function streamEvents(url: string, onMessage: (data: string) => void, onDone: (err?: Error) => void) {
	const evtSource = new EventSource(url);
	evtSource.onmessage = (e) => onMessage(e.data);
	evtSource.onerror = () => {
		onDone(new Error('SSE connection lost'));
		evtSource.close();
	};
	return () => evtSource.close();
}

