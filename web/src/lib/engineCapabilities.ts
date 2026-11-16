/**
 * App-level engine capabilities: the ONE place /api/engine/capabilities is
 * fetched. The engine's --capabilities document is the single source of
 * truth for the path-token vocabulary (picker chips, validation, examples);
 * Bragi owns no parallel token registry. Loaded by the app shell before any
 * page renders a path builder, so `pathTokens` is populated by the time the
 * builder mounts (the startup probe is fatal on the server, so a failed load
 * means the server is down, not "no engine").
 *
 * `loaded` flips to true ONLY on success: one failed fetch (server
 * restarting, transient 5xx) must not permanently block retries — the
 * shell's retry and the post-swap refresh re-run the GET until it lands.
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
let inflight: Promise<void> | null = null;

export function getEngineCapabilities(): EngineCapabilities | null {
	return caps;
}

/** True once a fetch has succeeded; a failed load is NOT "loaded". */
export function engineCapabilitiesLoaded(): boolean {
	return loaded;
}

async function fetchEngineCapabilities(): Promise<void> {
	const c = await get<EngineCapabilities>('/engine/capabilities');
	caps = c;
	setEngineTokens(c.path_tokens ?? []);
	loaded = true;
}

export function loadEngineCapabilities(): Promise<void> {
	if (loaded) return Promise.resolve();
	if (inflight) return inflight;
	inflight = fetchEngineCapabilities()
		.catch(() => {
			// Server down: the builder renders an empty palette and its
			// example stays unresolved; the save path is server-validated
			// anyway. Do NOT mark loaded: the next call retries.
		})
		.finally(() => {
			inflight = null;
		});
	return inflight;
}

/** Re-fetch after the engine binary changed (a settings save that swapped
 *  m4b_merge_binary): the old binary's token vocabulary is stale, so the
 *  registry must be reloaded before the builder validates against it. */
export function refreshEngineCapabilities(): Promise<void> {
	if (inflight) return inflight;
	inflight = fetchEngineCapabilities()
		.catch(() => {
			// Keep the last good registry: a failed refresh after a swap
			// must not blank the palette (server-validated save stands).
		})
		.finally(() => {
			inflight = null;
		});
	return inflight;
}
