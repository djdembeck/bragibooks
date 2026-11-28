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
 *
 * A .svelte.ts module: the cache is $state, so a $derived that reads
 * getEngineCapabilities() repaints when a swap-refresh lands new
 * capabilities (a plain module variable has no reactive dependency, so the
 * settings field list would keep showing the old vocabulary).
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

let loaded = $state(false);
let caps = $state<EngineCapabilities | null>(null);
let inflight: Promise<void> | null = null;
// The in-flight fetch was started by loadEngineCapabilities (the app
// shell's initial load), not by a refresh: a refresh that inherits a
// FAILED initial load must retry, because the initial load's own
// swallowed failure would otherwise leave the registry empty for the
// session.
let inflightIsInitialLoad = false;

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
	inflightIsInitialLoad = true;
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
	if (inflight) {
		// Inheriting the app shell's initial load: if that load fails,
		// ITS swallowed failure would end the retry chain, leaving the
		// registry empty for the session (a swap-save refresh is the
		// only remaining caller). Chain one re-fetch after a failed
		// settle; a successful initial load needs no follow-up.
		if (!loaded && inflightIsInitialLoad) {
			return inflight.then(() =>
				loaded ? Promise.resolve() : loadEngineCapabilities()
			);
		}
		return inflight;
	}
	inflightIsInitialLoad = false;
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
