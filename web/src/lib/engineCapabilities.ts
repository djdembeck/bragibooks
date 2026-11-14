/**
 * App-level engine capabilities: the ONE place /api/engine/capabilities is
 * fetched. The engine's --capabilities document is the single source of truth
 * for the path-token vocabulary (picker chips, validation, examples); Bragi
 * owns no parallel token registry. Loaded once by the app shell before any
 * page renders a path builder, so `pathTokens` is populated by the time the
 * builder mounts (the startup probe is fatal on the server, so a failed load
 * means the server is down, not "no engine").
 */
import { get } from '#lib/api.js';
import { setEngineTokens, type EngineToken } from '#lib/scheme.js';

let loaded = false;

export async function loadEngineCapabilities(): Promise<void> {
	if (loaded) return;
	loaded = true;
	try {
		const caps = await get<{ path_tokens?: EngineToken[] }>('/engine/capabilities');
		setEngineTokens(caps.path_tokens ?? []);
	} catch {
		// Server down: the builder renders an empty palette and its example
		// stays unresolved; the save path is server-validated anyway.
	}
}
