// Test stub for the SvelteKit `$app/state` virtual module, which the
// SvelteKit-less vitest environment cannot resolve. Components that only
// render chrome (Nav) read `page` from it; the stub is a fixed book page
// so rendering does not crash.
export const page = {
	url: new URL('http://localhost/books/1'),
	params: { id: '1' }
} as const;
