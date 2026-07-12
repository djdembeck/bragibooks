import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig } from 'vite';

const isDev = process.env.NODE_ENV === 'development';

export default defineConfig({
	plugins: [tailwindcss(), sveltekit()],
	server: isDev
		? {
				proxy: {
					'/api': 'http://localhost:8080',
				},
			}
		: undefined,
});