import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [svelte()],
	// Resolve Svelte's client runtime: `mount` and the rune modules under test
	// are unavailable in the server build that the default condition selects.
	resolve: { conditions: ['browser'] },
	test: {
		environment: 'happy-dom',
		include: ['src/**/*.test.ts'],
		// Naive-UTC parsing only diverges from a local parse away from UTC;
		// pin a non-UTC zone so a UTC runner cannot vacate the guard.
		env: { TZ: 'America/New_York' }
	}
});
