import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, loadEnv, type Plugin } from 'vite';

const KIT_CLIENT_RE = /@sveltejs[/\\]kit[/\\]src[/\\]runtime[/\\]client[/\\]client\.js$/;
const BROKEN_CAPTURE_SCROLL = 'history_info[index].scroll = scroll_state();';
const FIXED_CAPTURE_SCROLL = '(history_info[index] ??= {}).scroll = scroll_state();';

/**
 * SvelteKit's `capture_scroll` throws when a history entry has no
 * `history_info` record (sveltejs/kit#17500). A reload that skips
 * `beforeunload`/`visibilitychange` leaves the entries unpersisted, the next
 * same-document `popstate` moves to a recordless index, and the router stops
 * responding until a full reload. Patch the one line until the fix ships
 * upstream, then remove this plugin with the kit bump.
 */
function kitScrollFix(): Plugin {
	let applied = false;

	return {
		name: 'kit-scroll-fix',
		enforce: 'pre',
		transform(code, id) {
			if (!KIT_CLIENT_RE.test(id.split('?')[0])) return;
			if (!code.includes(BROKEN_CAPTURE_SCROLL)) {
				this.warn(
					'SvelteKit changed capture_scroll(); check whether sveltejs/kit#17500 is fixed and remove kitScrollFix'
				);
				return;
			}
			applied = true;
			return { code: code.replace(BROKEN_CAPTURE_SCROLL, FIXED_CAPTURE_SCROLL), map: null };
		},
		buildEnd() {
			if (this.environment?.name === 'client' && !applied) {
				this.error(
					'kitScrollFix did not patch the @sveltejs/kit client runtime; the sveltejs/kit#17500 workaround is inactive'
				);
			}
		},
	};
}

export default defineConfig(({ mode }) => {
	const env = loadEnv(mode, process.cwd(), '');
	const isDev = mode === 'development';
	const port = Number(env.VITE_PORT) || 5175;
	const proxyTarget = env.VITE_PROXY_TARGET || 'http://localhost:8000';

	return {
		plugins: [
			kitScrollFix(),
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
