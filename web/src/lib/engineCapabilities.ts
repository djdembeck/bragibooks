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

/** The full capabilities document, once loaded. */
export interface EngineCapabilities {
	schema_version?: number;
	resolver_version?: string;
	path_tokens?: EngineToken[];
	field_policies?: { values?: string[]; scalar_fields?: string[] };
	cover_policies?: { constraint?: string; variants?: string[] };
	chapter_policies?: { constraint?: string; variants?: string[] };
}

let loaded = false;
let caps: EngineCapabilities | null = null;

export function getEngineCapabilities(): EngineCapabilities | null {
	return caps;
}

export async function loadEngineCapabilities(): Promise<void> {
	if (loaded) return;
	loaded = true;
	try {
		const c = await get<EngineCapabilities>('/engine/capabilities');
		caps = c;
		setEngineTokens(c.path_tokens ?? []);
	} catch {
		// Server down: the builder renders an empty palette and its example
		// stays unresolved; the save path is server-validated anyway.
	}
}
