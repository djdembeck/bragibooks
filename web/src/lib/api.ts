import { cleanAdbError, type CleanedAdbError } from '$lib/adbError';

const API_BASE = '';

// Legacy cutover: the app no longer supports a stored admin token — remote
// access uses the server-generated External API key (never stored by the
// UI). A leftover `bragibooks_token` would otherwise be presented on every
// request and get 403 (a presented-but-invalid key is always rejected), so
// remove it from localStorage and expire the mirrored cookie once at load.
try {
	if (typeof window !== 'undefined') {
		window.localStorage.removeItem('bragibooks_token');
		document.cookie = 'bragibooks_token=; path=/; max-age=0';
	}
} catch {
	// storage unavailable — nothing to clear
}

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

// Same-origin UI auth: the UI never stores a key. It relies on the server's
// double-submit CSRF guard (loopback-LAN path) on the browser state.
function authHeaders(): Record<string, string> {
	try {
		const headers: Record<string, string> = {};
		// The server requires a double-submit CSRF token on state-changing
		// requests. Echo the server-set bragibooks_csrf cookie into
		// X-CSRF-Token; a request with no cookie (first hit) sends no value,
		// which the server rejects and the next UI action retries after the
		// GET (or any prior response) has set the cookie.
		const csrf = document.cookie
			.split('; ')
			.find((c) => c.startsWith('bragibooks_csrf='))
			?.slice('bragibooks_csrf='.length);
		if (csrf) {
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

