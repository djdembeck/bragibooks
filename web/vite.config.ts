import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
	const env = loadEnv(mode, process.cwd(), '');
	const isDev = mode === 'development';
	const port = Number(env.VITE_PORT) || 5175;
	const proxyTarget = env.VITE_PROXY_TARGET || 'http://localhost:8080';

	return {
		plugins: [tailwindcss(), sveltekit()],
		server: isDev
			? {
					port,
					strictPort: true,
					host: '0.0.0.0',
					proxy: {
						'/api': proxyTarget,
					},
				}
			: undefined,
	};
});
