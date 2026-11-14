import { flushSync, mount, unmount } from 'svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { IndexState, IndexStatus, Settings } from '#lib/types.js';
import { loadEngineCapabilities } from '#lib/engineCapabilities.js';
// vi.mock hoists above these, so the factory can name them directly.
import RefreshButtonStub from './RefreshButtonStub.svelte';

const { getMock, postMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	postMock: vi.fn()
}));

vi.mock('#lib/api.js', () => ({
	get: getMock,
	post: postMock,
	put: vi.fn(),
	del: vi.fn(),
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
		// The app-level capabilities module caches after its first load, so
		// the capabilities mock must be identical across every test; the
		// settings mock is re-applied per test in mountWith.
		getMock.mockImplementation(async (path: string) => {
			if (path === '/engine/capabilities')
				return {
					field_policies: { scalar_fields: ALL_FIELDS.filter((f) => f !== 'cover') },
					path_tokens: [{ name: 'title', value: 'title', example: 'The Book' }]
				};
			if (path === '/index')
				return indexState('never', 0);
			if (path === '/output-profiles')
				return { profiles: [] };
			return settingsFixture();
		});
		await loadEngineCapabilities();
	});

	afterEach(() => {
		unmount(component);
		target.remove();
		vi.useRealTimers();
	});

	async function mountWith(fields: unknown, overrides: Record<string, unknown> = {}) {
		// Re-aim the /settings response at the selection under test.
		getMock.mockImplementation(async (path: string) => {
			if (path === '/settings')
				return { ...settingsFixture(), metadata_fields: fields, metadata_chapters: 'write', ...overrides };
			if (path === '/engine/capabilities')
				return {
					field_policies: { scalar_fields: ALL_FIELDS.filter((f) => f !== 'cover') },
					path_tokens: [{ name: 'title', value: 'title', example: 'The Book' }]
				};
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
