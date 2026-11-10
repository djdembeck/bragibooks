import { svelte } from '@sveltejs/vite-plugin-svelte';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [svelte()],
	// Resolve Svelte's client runtime: `mount` and the rune modules under test
	// are unavailable in the server build that the default condition selects.
	resolve: {
		conditions: ['browser'],
		// SvelteKit virtual modules: the SvelteKit-less test environment
		// cannot resolve them, so they alias to a static stub (tests that
		// exercise navigation mock the module themselves).
		alias: {
			'$app/navigation': fileURLToPath(new URL('./src/test/app-navigation.ts', import.meta.url))
		}
	},
	test: {
		environment: 'happy-dom',
		include: ['src/**/*.test.ts'],
		// Naive-UTC parsing only diverges from a local parse away from UTC;
		// pin a non-UTC zone so a UTC runner cannot vacate the guard.
		env: { TZ: 'America/New_York' }
	}
});
