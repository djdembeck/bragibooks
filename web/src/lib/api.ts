const API_BASE = '';

export async function get<T>(path: string, init?: RequestInit): Promise<T> {
	const res = await fetch(`${API_BASE}/api${path}`, {
		...init,
		headers: { Accept: 'application/json', ...(init?.headers ?? {}) }
	});
	if (!res.ok) {
		const text = await res.text();
		throw new Error(`API ${res.status}: ${text}`);
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

export function streamNDJSON(url: string, onLine: (line: string) => void, onDone: (err?: Error) => void) {
	const abort = new AbortController();
	fetch(url, { signal: abort.signal })
		.then(async (res) => {
			const reader = res.body!.getReader();
			const decoder = new TextDecoder();
			let buffer = '';
			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });
				const lines = buffer.split('\n');
				buffer = lines.pop()!;
				for (const line of lines) {
					if (line.trim()) onLine(line.trim());
				}
			}
			if (buffer.trim()) onLine(buffer.trim());
		})
		.catch((err) => onDone(err))
		.finally(() => onDone());
	return () => abort.abort();
}
