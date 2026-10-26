import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [svelte()],
	// Resolve Svelte's client runtime: `mount` and the rune modules under test
	// are unavailable in the server build that the default condition selects.
	resolve: { conditions: ['browser'] },
	test: {
		environment: 'happy-dom',
		include: ['src/**/*.test.ts']
	}
});
