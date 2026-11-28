import { get } from 'svelte/store';
import { describe, expect, it, vi } from 'vitest';

// The module keeps app-wide singleton state (loaded / caps / inflight), so
// every scenario re-imports a fresh module instance through resetModules.
// Only #lib/api.js is mocked; the real scheme token registry is exercised
// (setEngineTokens runs on a successful fetch).

const getMock = vi.fn();
vi.mock('#lib/api.js', () => ({ get: getMock }));

const CAPS = {
	schema_version: 1,
	resolver_version: '1',
	path_tokens: [{ name: 'title', value: 'title', example: 'T' }]
};

// Dynamic import is intentional: each scenario needs a FRESH module
// instance (the singleton loaded/caps/inflight state is module-level),
// which a static import cannot provide.
async function freshModule() {
	vi.resetModules();
	getMock.mockReset();
	const mod = await import('../engineCapabilities.svelte.js');
	return mod;
}

describe('engineCapabilities load/retry/refresh', () => {
	it('does not mark loaded on failure, so the next call retries', async () => {
		const mod = await freshModule();
		getMock.mockRejectedValueOnce(new Error('server down'));
		await mod.loadEngineCapabilities();
		// A failed fetch is NOT "loaded": the palette is empty and the
		// registry is not poisoned.
		expect(mod.engineCapabilitiesLoaded()).toBe(false);
		expect(mod.getEngineCapabilities()).toBeNull();

		// The retry must issue a SECOND fetch (the old code set loaded
		// before the GET and swallowed the failure, so this call would
		// have been a no-op).
		getMock.mockResolvedValueOnce(CAPS);
		await mod.loadEngineCapabilities();
		expect(getMock).toHaveBeenCalledTimes(2);
		expect(mod.engineCapabilitiesLoaded()).toBe(true);
		expect(mod.getEngineCapabilities()?.path_tokens).toHaveLength(1);
	});

	it('never fabricates capabilities from a failed load', async () => {
		// A failed fetch must leave the document null (an empty registry
		// is a capability gap the E0 scan must tolerate, not data to
		// invent): the builder renders an empty palette and the save
		// path is server-validated.
		const mod = await freshModule();
		getMock.mockRejectedValue(new Error('down'));
		await mod.loadEngineCapabilities();
		expect(mod.getEngineCapabilities()).toBeNull();
		expect(mod.engineCapabilitiesLoaded()).toBe(false);
	});

	it('a swap-save refresh retries a failed initial load', async () => {
		const mod = await freshModule();
		// The shell's initial load fails (server restarting): nothing is
		// loaded and the registry is empty.
		getMock.mockRejectedValueOnce(new Error('initial down'));
		await mod.loadEngineCapabilities();
		expect(mod.engineCapabilitiesLoaded()).toBe(false);
		// A swap-save refresh must not inherit the initial load's
		// swallowed failure silently: it chains a re-fetch after the
		// failed settle.
		getMock.mockResolvedValueOnce(CAPS);
		await mod.refreshEngineCapabilities();
		expect(getMock).toHaveBeenCalledTimes(2);
		expect(mod.engineCapabilitiesLoaded()).toBe(true);
		expect(mod.getEngineCapabilities()?.path_tokens).toHaveLength(1);
	});

	it('refresh re-fetches after a swap and keeps the last good registry on failure', async () => {
		const mod = await freshModule();
		getMock.mockResolvedValueOnce(CAPS);
		await mod.loadEngineCapabilities();
		expect(getMock).toHaveBeenCalledTimes(1);

		// A swap refresh must force a new GET even though loaded is true.
		const otherTokens = [{ name: 'author', value: 'author', example: 'A' }];
		getMock.mockRejectedValueOnce(new Error('new binary down'));
		await mod.refreshEngineCapabilities();
		expect(getMock).toHaveBeenCalledTimes(2);
		// The failure must not blank the registry: the last good token
		// set stands (the server-validated save stands). Dynamic import:
		// the token registry lives in the SAME fresh scheme.js instance
		// the re-imported engineCapabilities module registered into.
		const { engineTokens } = await import('../scheme.js');
		expect(get(engineTokens).map((t) => t.name)).toEqual(['title']);
	});
});
