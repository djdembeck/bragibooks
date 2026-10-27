import { defineConfig } from 'vitest/config';

export default defineConfig({
	test: {
		include: ['src/**/*.test.ts'],
		// Naive-UTC parsing only diverges from a local parse away from UTC;
		// pin a non-UTC zone so a UTC runner cannot vacate the guard.
		env: { TZ: 'America/New_York' }
	}
});
