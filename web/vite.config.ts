import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
	const env = loadEnv(mode, process.cwd(), '');
	const isDev = mode === 'development';
	const port = Number(env.VITE_PORT) || 5175;
	const proxyTarget = env.VITE_PROXY_TARGET || 'http://localhost:8080';

	return {
		plugins: [
			tailwindcss(),
			sveltekit({
				adapter: adapter({
					pages: 'build',
					assets: 'build',
					fallback: 'index.html',
					precompress: false,
					strict: true,
				}),
			}),
		],
		server: isDev
			? {
					port,
					strictPort: true,
					host: '0.0.0.0',
					proxy: {
						'/api': proxyTarget,
						// Extracted source covers are written by the Go server
						// beside its database; Vite must forward them in dev.
						'/covers': proxyTarget
					},
				}
			: undefined,
	};
});
