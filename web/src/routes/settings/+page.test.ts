import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { IndexState, IndexStatus, InvalidOutputProfile, OutputProfile, Settings } from '#lib/types.js';
// vi.mock hoists above these, so the factory can name them directly.
import RefreshButtonStub from './RefreshButtonStub.svelte';

const { getMock, postMock, putMock, delMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn(),
	putMock: vi.fn(),
	delMock: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: postMock,
	put: putMock,
	del: delMock,
	ApiError: class ApiError extends Error {}
}));

// The settings page composes many presentational components; the poll and
// refresh contracts under test live in the script, so the components stub
// out. Button renders just enough (the children snippet, a clickable
// button that respects `disabled`/`loading`) to drive the Refresh flow.
vi.mock('#lib/components/index.js', () => ({
	PageHeader: () => null,
	Button: RefreshButtonStub,
	Alert: () => null,
	ErrorDetail: () => null,
	Skeleton: () => null,
	Spinner: () => null,
	DirPicker: () => null,
	Modal: () => null,
	PathFormatBuilder: () => null
}));

// The real scheme module's token store is app-wide state the page tests
// never seed (the app's capabilities loader does). Without a token, every
// placeholder in a scheme reads as an unsupported token and the form
// validation gates the save — so stub the validator with permissive,
// test-controlled functions and keep the parse/serialize contract real.
vi.mock('#lib/scheme.js', () => ({
	parseParts: (t: string) => [{ kind: 'text', value: t }],
	serializeParts: (parts: { kind: string; key?: string; value?: string }[]) =>
		parts.map((p) => (p.kind === 'sep' ? '/' : p.kind === 'token' ? `{${p.key}}` : p.value)).join(''),
	validateScheme: () => ({ errors: [], warnings: [] }),
	trimSegment: (s: string) => s.trim()
}));

// The real capabilities module caches app-wide (one load per app). The
// tests need a KNOWN, resettable state — including the failure shape the
// real module produces when the fetch fails (null) — so stub it with a
// settable holder.
const engineCaps = vi.hoisted(() => {
	const holder: { value: unknown } = { value: null };
	return {
		holder,
		set: (c: unknown) => {
			holder.value = c;
		},
		reset: () => {
			holder.value = null;
		}
	};
});
vi.mock('#lib/engineCapabilities.svelte.js', () => ({
	getEngineCapabilities: () => engineCaps.holder.value,
	loadEngineCapabilities: async () => {}
}));

import SettingsPage from './+page.svelte';

function settingsFixture(): Settings {
	return {
		m4b_merge_binary: 'm4b-merge',
		input_dir: '/in',
		output_dir: '/out',
		completed_dir: '',
		num_cpus: 1,
		output_scheme: '{author}/{title}',
		region: 'us',
		indexing_enabled: true
	};
}

function indexState(status: IndexState['status'], generation: number): IndexStatus {
	return {
		enabled: true,
		state: {
			root: '/in',
			entries: 1,
			last_run_at: '',
			status,
			detail: '',
			generation
		}
	};
}

describe('settings index in-flight poll', () => {
	let target: HTMLElement;
	let component: Record<string, any>;

	beforeEach(() => {
		vi.useFakeTimers();
		getMock.mockReset();
		postMock.mockReset();
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture();
			if (path === '/output-profiles') return { profiles: [] };
			if (path === '/index') return indexState('queued', 1);
			return {};
		});
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		unmount(component);
		target.remove();
		vi.useRealTimers();
	});

	async function mountPage() {
		component = mount(SettingsPage, { target });
		flushSync();
		// Settle the mount-time load chain: settings -> index status.
		await vi.advanceTimersByTimeAsync(0);
	}

	function indexCallCount(): number {
		return getMock.mock.calls.filter((c) => c[0] === '/index').length;
	}

	it('polls while the scan is in flight and stops at its own terminal', async () => {
		await mountPage();
		// The initial /index read (queued, generation 1) started the
		// poller; each 1 s tick issues one more read.
		expect(indexCallCount()).toBe(1);
		await vi.advanceTimersByTimeAsync(3000);
		expect(indexCallCount()).toBe(4);

		// The scan's own generation reaches a terminal: the next tick
		// observes it and the poller stops.
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexState('done', 1) : (getMock.mock.calls.length ? {} : settingsFixture())
		);
		await vi.advanceTimersByTimeAsync(1000);
		const stoppedAt = indexCallCount();
		expect(stoppedAt).toBeGreaterThanOrEqual(5);
		// No further ticks: the loop ended.
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(stoppedAt);
	});

	it('follows a NEW in-flight generation to its terminal, then stops', async () => {
		await mountPage();
		// The captured scan (gen 1) never lands: a newer scan (gen 2) is
		// in flight. The poller keeps going — it must not stop on the
		// assumption that only gen 1 will ever complete.
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexState('running', 2) : {}
		);
		await vi.advanceTimersByTimeAsync(2000);
		expect(indexCallCount()).toBe(3);

		// The new scan reaches its terminal: the page state mirrors it
		// and the poll stops (the effect re-runs on the terminal state).
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexState('done', 2) : {}
		);
		await vi.advanceTimersByTimeAsync(1000);
		const stoppedAt = indexCallCount();
		expect(stoppedAt).toBe(4);
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(stoppedAt);
	});

	it('does not poll when the initial status is already terminal', async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture();
			if (path === '/output-profiles') return { profiles: [] };
			if (path === '/index') return indexState('done', 1);
			return {};
		});
		await mountPage();
		expect(indexCallCount()).toBe(1);
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(1);
	});

	it('ignores the initial /index response that resolves after a manual refresh started', async () => {
		// The initial /index read is deferred: it resolves AFTER the manual
		// refresh starts, carrying the PRE-refresh terminal state. Applying
		// it would overwrite the queued running state with the stale
		// terminal and strand Refresh disabled forever.
		const { promise: initialRead, resolve: resolveInitial } =
			Promise.withResolvers<IndexStatus>();
		let indexCalls = 0;
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture();
			if (path === '/output-profiles') return { profiles: [] };
			if (path === '/index') {
				indexCalls++;
				if (indexCalls === 1) return initialRead; // initial effect's read
				return indexState('queued', 2); // the poller sees the in-flight scan
			}
			return {};
		});
		postMock.mockResolvedValue({ state: indexState('queued', 2).state });
		await mountPage();
		expect(indexCallCount()).toBe(1);

		const refresh = [...target.querySelectorAll<HTMLButtonElement>('button')].find(
			(b) => b.textContent?.trim() === 'Refresh now'
		);
		if (!refresh) throw new Error('Refresh now button not rendered');
		refresh.click();
		await vi.advanceTimersByTimeAsync(0);
		// The queued running state is visible and Refresh is in flight.
		expect(target.textContent).toContain('Index queued — starting…');
		expect((refresh as HTMLButtonElement).disabled).toBe(true);

		// One poll tick observes the in-flight scan and keeps polling.
		await vi.advanceTimersByTimeAsync(1000);
		expect(indexCallCount()).toBe(2);

		// NOW the initial read resolves with the stale pre-refresh
		// terminal (done, generation 1). It must be ignored.
		resolveInitial(indexState('done', 1));
		await vi.advanceTimersByTimeAsync(0);
		// The queued running state is still displayed (the stale response
		// did not overwrite it), the poller is still alive, and Refresh is
		// still busy (not stranded in a terminal state of its own scan).
		expect(target.textContent).toContain('Index queued — starting…');
		expect(indexCallCount()).toBe(2);
		expect((refresh as HTMLButtonElement).disabled).toBe(true);

		// The refresh's OWN generation reaches its terminal: the next tick
		// observes it, the poller stops, and Refresh comes back.
		getMock.mockImplementation(async (path: string) =>
			path === '/index' ? indexState('done', 2) : {}
		);
		await vi.advanceTimersByTimeAsync(1000);
		const stoppedAt = indexCallCount();
		expect(stoppedAt).toBe(3);
		expect((refresh as HTMLButtonElement).disabled).toBe(false);
		await vi.advanceTimersByTimeAsync(5000);
		expect(indexCallCount()).toBe(stoppedAt);
	});

	it('shows the interruption (with Refresh guidance) for an interrupted terminal with an empty last_run_at', async () => {
		// A first scan interrupted during shutdown is terminal with an
		// empty last_run_at: the line must say the index was interrupted
		// and point at Refresh, not fall through to "Not indexed yet".
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture();
			if (path === '/output-profiles') return { profiles: [] };
			if (path === '/index') return indexState('interrupted', 1); // last_run_at ''
			return {};
		});
		await mountPage();
		expect(target.textContent).toContain('Last index was interrupted');
		expect(target.textContent).toContain('Use Refresh to finish');
		expect(target.textContent).not.toContain('Not indexed yet');
	});
});

// The metadata write selection (#518) must distinguish the three states the
// server round-trips: null (never saved = default, every supported field
// written), a present list (write exactly those), and [] (write nothing).
// The checkboxes are the only visible representation of that distinction.
describe('settings metadata write selection rendering', () => {
	let target: HTMLElement;
	let component: Record<string, any>;
	const ALL_FIELDS = [
		'title', 'subtitle', 'authors', 'narrators',
		'series', 'description', 'genres', 'year', 'cover'
	];

	beforeEach(async () => {
		vi.useFakeTimers();
		getMock.mockReset();
		target = document.createElement('div');
		document.body.appendChild(target);
		// The capabilities stub is reset per test: a stock install
		// reports every scalar field the app knows about (cover is
		// policy-only, not a scalar field).
		engineCaps.set({
			field_policies: { scalar_fields: ALL_FIELDS.filter((f) => f !== 'cover') },
			path_tokens: [{ name: 'title', value: 'title', example: 'The Book' }]
		});
		getMock.mockImplementation(async (path: string) => {
			if (path === '/index')
				return indexState('never', 0);
			if (path === '/output-profiles')
				return { profiles: [] };
			return settingsFixture();
		});
	});

	afterEach(() => {
		unmount(component);
		target.remove();
		vi.useRealTimers();
	});

	async function mountWith(fields: unknown, overrides: Record<string, unknown> = {}) {
		// Re-aim the /settings response at the selection under test
		// (the capabilities stub from beforeEach stays in effect).
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings')
				return { ...settingsFixture(), metadata_fields: fields, metadata_chapters: 'write', ...overrides };
			if (path === '/index')
				return indexState('never', 0);
			if (path === '/output-profiles')
				return { profiles: [] };
			return settingsFixture();
		});
		component = mount(SettingsPage, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(250); // delayedLoad
	}

	function checkedIds(): Set<string> {
		return new Set(
			[...target.querySelectorAll<HTMLInputElement>('.metadata-field-grid input[type=checkbox]')]
				.filter((c) => c.checked)
				.map((c) => c.id.replace('meta_field_', ''))
		);
	}

	it('renders every field checked for the null (default) selection', async () => {
		await mountWith(null);
		expect(checkedIds().size).toBe(ALL_FIELDS.length);
		expect(checkedIds().has('title')).toBe(true);
		expect(checkedIds().has('cover')).toBe(true);
		// The "nothing is selected" banner must NOT appear for the default.
		expect(target.textContent).not.toContain('Nothing is selected');
	});

	it('renders exactly the selected fields for an explicit list', async () => {
		await mountWith(['title']);
		const ids = checkedIds();
		expect(ids.size).toBe(1);
		expect(ids.has('title')).toBe(true);
		expect(ids.has('subtitle')).toBe(false);
	});

	it('reflects cover in the saved list (not filtered out of the checkbox state)', async () => {
		await mountWith(['title', 'cover']);
		const ids = checkedIds();
		expect(ids.size).toBe(2);
		expect(ids.has('title')).toBe(true);
		expect(ids.has('cover')).toBe(true);
	});

	it('renders no fields checked and the banner for an empty selection', async () => {
		await mountWith([]);
		expect(checkedIds().size).toBe(0);
		expect(target.textContent).toContain('Nothing is selected');
	});

	it('materializes the full default on the first checkbox edit from null', async () => {
		await mountWith(null);
		// Unchecking ONE field from the default must leave every OTHER
		// field checked — not the empty selection (which would disable
		// all metadata writes on save).
		const title = target.querySelector<HTMLInputElement>('#meta_field_title');
		expect(title).not.toBeNull();
		title!.checked = false;
		title!.dispatchEvent(new Event('change', { bubbles: true }));
		flushSync();
		const ids = checkedIds();
		expect(ids.size).toBe(ALL_FIELDS.length - 1);
		expect(ids.has('title')).toBe(false);
		for (const f of ALL_FIELDS)
			if (f !== 'title')
				expect(ids.has(f), `${f} must stay checked`).toBe(true);
	});

	it('unchecking every field from the default yields the empty (none-selected) selection', async () => {
		await mountWith(null);
		for (const f of ALL_FIELDS) {
			const input = target.querySelector<HTMLInputElement>(`#meta_field_${f}`);
			expect(input, f).not.toBeNull();
			input!.checked = false;
			input!.dispatchEvent(new Event('change', { bubbles: true }));
		}
		flushSync();
		expect(checkedIds().size).toBe(0);
		expect(target.textContent).toContain('Nothing is selected');
	});

	it('shows the capability-missing notice (not a Cover-only grid) when the engine reports no scalar fields', async () => {
		// The failure shape the real module produces when the fetch
		// fails: getEngineCapabilities() is null for the session.
		engineCaps.reset();
		await mountWith(null);
		expect(target.textContent).toContain('does not report per-field write support');
		expect(target.querySelectorAll('.metadata-field-grid input[type=checkbox]').length).toBe(0);
	});

	it('renders the pinned controls read-only with a deployment hint', async () => {
		await mountWith(['title'], {
			metadata_fields_pinned: true,
			metadata_chapters_pinned: true
		});
		// The pinned value is still shown (checked), but the controls are
		// disabled so a save cannot be made to look like an edit.
		expect(checkedIds().size).toBe(1);
		expect(checkedIds().has('title')).toBe(true);
		const checkboxes = [...target.querySelectorAll<HTMLInputElement>('.metadata-field-grid input[type=checkbox]')];
		expect(checkboxes.length).toBe(ALL_FIELDS.length);
		for (const c of checkboxes)
			expect(c.disabled).toBe(true);
		const radios = [...target.querySelectorAll<HTMLInputElement>('input[name=chapters_policy]')];
		expect(radios.length).toBe(2);
		for (const r of radios)
			expect(r.disabled).toBe(true);
		expect(target.textContent).toContain('PROCESSING_METADATA_FIELDS');
		expect(target.textContent).toContain('PROCESSING_METADATA_CHAPTERS');
	});
});

// The repair list (profiles the current engine rejects) is server-derived:
// the page must mirror it from the wire, not track it client-side. The PUT
// save copies the list from the response (an engine swap re-derives it
// server-side in the same save), and a profile mutation re-fetches GET
// /settings on the same reload path.
describe('settings invalid output profiles repair list', () => {
	let target: HTMLElement;
	let component: Record<string, any>;

	const invalidProfile: InvalidOutputProfile = {
		id: 7,
		name: 'Wide',
		format: '{series_position}/{title}',
		error: 'unsupported token'
	};
	const validProfile: OutputProfile = {
		id: 8,
		name: 'Shelf',
		output_dir: '/shelf',
		output_scheme: '{author}/{title}',
		created_at: '2026-01-01T00:00:00Z',
		updated_at: '2026-01-01T00:00:00Z'
	};

	function invalidListRows(): number {
		return target.querySelectorAll('.profile-list--invalid .profile-row').length;
	}

	beforeEach(() => {
		vi.useFakeTimers();
		getMock.mockReset();
		postMock.mockReset();
		putMock.mockReset();
		delMock.mockReset();
		// {author} is in the engine token vocabulary, so the default
		// {author}/{title} scheme passes the form validation that gates
		// the save; the PUT/DEL mocks answer the profile mutations.
		engineCaps.set({
			field_policies: { scalar_fields: ['title'] },
			path_tokens: [
				{ name: 'title', value: 'title', example: 'The Book' },
				{ name: 'author', value: 'author', example: 'The Author' }
			]
		});
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings') return settingsFixture();
			if (path === '/output-profiles') return { profiles: [] };
			return {};
		});
		putMock.mockResolvedValue({});
		delMock.mockResolvedValue({});
		target = document.createElement('div');
		document.body.appendChild(target);
	});

	afterEach(() => {
		unmount(component);
		target.remove();
		vi.useRealTimers();
	});

	async function mountPage() {
		component = mount(SettingsPage, { target });
		flushSync();
		await vi.advanceTimersByTimeAsync(250); // delayedLoad
	}

	it('copies invalid_output_profiles from the PUT response into the repair list', async () => {
		await mountPage();
		// Clean start: the repair list is not rendered.
		expect(invalidListRows()).toBe(0);

		// The save (e.g. an engine-binary swap) re-derives the list
		// server-side and returns it in the PUT response; the page must
		// copy it into the visible repair list from that response, not
		// wait for a follow-up GET.
		putMock.mockResolvedValue({ ...settingsFixture(), invalid_output_profiles: [invalidProfile] });
		const save = [...target.querySelectorAll<HTMLButtonElement>('button')].find(
			(b) => b.textContent?.includes('Save settings')
		);
		if (!save) throw new Error('Save settings button not rendered');
		save.click();
		await vi.advanceTimersByTimeAsync(0);
		expect(putMock).toHaveBeenCalledWith('/settings', expect.anything());
		expect(invalidListRows()).toBe(1);
		expect(target.textContent).toContain('Wide');
		expect(target.textContent).toContain('{series_position}/{title}');
	});

	it('refreshes the repair list from GET /settings after a profile mutation', async () => {
		// The server's verdict tracks the delete: one rejected profile
		// until the profile is gone, then clean.
		getMock.mockImplementation(async (path: string) => {
			const deleted = delMock.mock.calls.length > 0;
			if (path === '/settings')
				return { ...settingsFixture(), invalid_output_profiles: deleted ? [] : [invalidProfile] };
			if (path === '/output-profiles')
				return { profiles: deleted ? [] : [validProfile] };
			return {};
		});
		await mountPage();
		expect(invalidListRows()).toBe(1);

		// Delete the only profile: the mutation path must re-fetch
		// /output-profiles AND /settings, and the repair list must drop to
		// the server's new (empty) verdict.
		const deleteBtn = target.querySelector<HTMLButtonElement>(
			'button[aria-label="Delete profile Shelf"]'
		);
		if (!deleteBtn) throw new Error('Delete profile button not rendered');
		deleteBtn.click();
		await vi.advanceTimersByTimeAsync(0);
		const confirm = target.querySelector<HTMLButtonElement>('#profile-delete-confirm');
		if (!confirm) throw new Error('delete confirmation not rendered');
		confirm.click();
		await vi.advanceTimersByTimeAsync(0);
		expect(delMock).toHaveBeenCalledWith('/output-profiles/8');
		expect(invalidListRows()).toBe(0);
		expect(target.textContent).not.toContain('Wide');
	});
});
