<script lang="ts">
	import { get, post, put, del } from '#lib/api.js';
	import { goto } from '$app/navigation';
	import { page as pageStore } from '$app/state';
	import type { BooksListResponse, BookWithPeople, OutputProfile, OutputProfilesResponse } from '#lib/types.js';
	import { PageHeader, StatusBadge, Skeleton, EmptyState, Alert, ErrorDetail, Button, CoverArt, OutputProfileSelector, Modal } from '#lib/components/index.js';
	import { formatRuntime } from '#lib/types.js';
	import { delayedLoad, type DelayedLoadState } from '#lib/delayedLoad.svelte.js';
	import { toCleanedError, type CleanedAdbError } from '#lib/adbError.js';

	const limit = 20;
	const filters: { value: string; label: string }[] = [
		{ value: 'all', label: 'All' },
		{ value: 'pending', label: 'Pending' },
		{ value: 'matched', label: 'Matched' },
		{ value: 'processing', label: 'Processing' },
		{ value: 'done', label: 'Done' },
		{ value: 'error', label: 'Error' },
	];

	let filter = $state('all');
	let currentPage = $state(0);
	let q = $state('');
	let response = $state<BooksListResponse | null>(null);
	const dl: DelayedLoadState = delayedLoad({ delay: 200 });

	const validFilters = new Set(filters.map((item) => item.value));

	let searchInput = $state<HTMLInputElement | null>(null);
	// Uncontrolled-style input value; the debounce below commits it to `q`.
	let inputValue = $state('');

	// Selection model: a "select all matching" baseline (the server-capped
	// ID list) plus row-level edits layered on top of it.
	//   effective selection = (baseline − excluded) ∪ added
	let allMatchedIds = $state<Set<number> | null>(null);
	let allMatchedTotal = $state(0);
	// The all_ids payload is capped server-side; when true the baseline
	// holds only the first 1,000 matches, so the UI must say so.
	let allMatchedTruncated = $state(false);
	// Rows the operator unchecked after a select-all.
	let excludedIds = $state<Set<number>>(new Set());
	// Rows the operator checked individually (with no select-all baseline).
	let addedIds = $state<Set<number>>(new Set());
	let selectingAll = $state(false);
	let selectAllControl = $state<HTMLInputElement | null>(null);
	// Which bulk operation is running (its button shows progress); null = idle.
	let bulkActing = $state<null | 'requeue' | 'done' | 'remove'>(null);
	// Books finished by the running sequential loop — shown as "n of m".
	let bulkProgress = $state(0);
	// Short-lived success line after a completed bulk operation.
	let bulkSuccess = $state<string | null>(null);
	let successTimer: ReturnType<typeof setTimeout> | null = null;
	let bulkError = $state<CleanedAdbError | null>(null);
	// Which operation produced bulkError, so Retry re-runs the right one.
	let bulkErrorAction = $state<'select' | 'done' | 'remove'>('select');
	// Removal confirmation dialog.
	let confirmRemoveOpen = $state(false);
	let removeList = $state<BookWithPeople[]>([]);
	let removeListLoading = $state(false);
	let profiles = $state<OutputProfile[]>([]);
	let profilesError = $state<string | null>(null);
	// null = "Global settings"; set only when the user picks a saved profile.
	let selectedProfileId = $state<number | null>(null);

	async function loadProfiles() {
		// Independent of the catalog load: a profile-list failure must not
		// fail the page; the selector simply offers Global settings only.
		try {
			const res = await get<OutputProfilesResponse>('/output-profiles');
			profiles = res.profiles || [];
			profilesError = null;
		} catch {
			profilesError = 'Saved profiles unavailable — queuing uses global settings.';
		}
	}
	function normalizedFilter(value: string | null): string {
		return value && validFilters.has(value) ? value : 'all';
	}

	function normalizedPage(value: string | null): number {
		const parsed = Number(value);
		return Number.isInteger(parsed) && parsed >= 0 ? parsed : 0;
	}

	function updateUrl(nextFilter: string, nextPage: number, nextQ?: string) {
		const params = new URLSearchParams(pageStore.url.search);
		if (nextFilter === 'all') params.delete('status');
		else params.set('status', nextFilter);
		if (nextPage > 0) params.set('page', String(nextPage));
		else params.delete('page');
		if (nextQ !== undefined) {
			if (nextQ) params.set('q', nextQ);
			else params.delete('q');
		}
		const query = params.toString();
		void goto(`/books${query ? `?${query}` : ''}`, {
			replace: true,
			reset: false
		});
	}

	function buildBooksQuery(extra = '') {
		const params = new URLSearchParams();
		if (filter !== 'all') params.set('status', filter);
		if (q) params.set('q', q);
		params.set('page', String(currentPage));
		params.set('limit', String(limit));
		return `/books?${params.toString()}${extra}`;
	}

	async function loadBooks() {
		void loadProfiles();
		await dl.run(async () => {
			const nextResponse = await get<BooksListResponse>(buildBooksQuery());
			const lastPage = Math.max(0, Math.ceil(nextResponse.total / limit) - 1);
			if (currentPage > lastPage) {
				currentPage = lastPage;
				updateUrl(filter, lastPage);
				return;
			}
			response = nextResponse;
		});
	}

	function setFilter(value: string) {
		filter = normalizedFilter(value);
		currentPage = 0;
		clearSelection(); // A new filter redefines "all matching" — the old selection is stale.
		updateUrl(filter, 0);
	}

	function setPage(nextPage: number) {
		currentPage = Math.max(0, Math.min(nextPage, totalPages - 1));
		updateUrl(filter, currentPage);
	}

	// Debounced search — plain timer in the input handler (not a reactive
	// effect, so it can't be re-armed by state churn). Fires 300ms after the
	// user stops typing: commit q, reset the page, and drive the ?q= nav.
	let searchTimer: ReturnType<typeof setTimeout> | null = null;
	function onSearchInput() {
		if (searchTimer) clearTimeout(searchTimer);
		searchTimer = setTimeout(() => {
			const next = inputValue.trim();
			if (next === q) return;
			q = next;
			currentPage = 0;
			clearSelection(); // A new query redefines "all matching" — the old selection is stale.
			updateUrl(filter, 0, next);
		}, 300);
	}
	$effect(() => () => {
		if (searchTimer) clearTimeout(searchTimer);
	});

	// Select all matching books for the current filter + search
	async function selectAllMatching() {
		selectingAll = true;
		bulkError = null;
		// Snapshot the request criteria: a filter/search change while the
		// request is in flight clears the selection, and restoring this
		// response afterwards would resurrect IDs that no longer match.
		const requestedFilter = filter;
		const requestedQ = q;
		try {
			const params = new URLSearchParams();
			if (requestedFilter !== 'all') params.set('status', requestedFilter);
			if (requestedQ) params.set('q', requestedQ);
			params.set('all_ids', '1');
			const res = await get<{ ids: number[]; total: number; truncated: boolean }>(`/books?${params.toString()}`);
			if (filter !== requestedFilter || q !== requestedQ) return;
			if (!res || !Array.isArray(res.ids)) throw new Error('Could not load matching book IDs.');
			allMatchedIds = new Set(res.ids);
			// The server caps the all_ids payload: total/truncated make the
			// cut explicit so the UI never treats a capped list as the whole
			// matching set (finding: F21).
			allMatchedTotal = res.total ?? res.ids.length;
			allMatchedTruncated = Boolean(res.truncated);
			// The new baseline redefines the set; individual edits no longer apply.
			excludedIds = new Set();
			addedIds = new Set();
		} catch (e) {
			if (filter !== requestedFilter || q !== requestedQ) return;
			bulkError = toCleanedError(e);
			allMatchedIds = null;
		} finally {
			selectingAll = false;
			selectAllOptimistic = null;
		}
	}

	function deselectAllMatching() {
		if (!allMatchedIds) return;
		bulkError = null;
		allMatchedIds = null;
		allMatchedTotal = 0;
		allMatchedTruncated = false;
		addedIds = new Set();
	}

	function clearSelection() {
		allMatchedIds = null;
		allMatchedTotal = 0;
		allMatchedTruncated = false;
		excludedIds = new Set();
		addedIds = new Set();
		bulkError = null;
		selectedProfileId = null; // The batch it applied to no longer exists.
	}

	// Effective selection for a book: the baseline minus exclusions,
	// plus individually checked rows.
	function isBookSelected(book: BookWithPeople): boolean {
		if (allMatchedIds) {
			if (excludedIds.has(book.id)) return false;
			if (allMatchedIds.has(book.id)) return true;
			return addedIds.has(book.id);
		}
		return addedIds.has(book.id);
	}

	function toggleBook(book: BookWithPeople) {
		const next = new Set(addedIds);
		const nextExcluded = new Set(excludedIds);
		if (isBookSelected(book)) {
			if (allMatchedIds?.has(book.id)) {
				// Uncheck a baseline row: record the exclusion.
				nextExcluded.add(book.id);
				next.delete(book.id);
			} else {
				next.delete(book.id);
			}
		} else {
			if (allMatchedIds?.has(book.id)) {
				// Re-check a previously excluded baseline row.
				nextExcluded.delete(book.id);
			}
			next.add(book.id);
		}
		addedIds = next;
		excludedIds = nextExcluded;
	}

	// Count of selected books across the WHOLE matching set (not just the
	// visible page) — the size the bulk requeue will actually send.
	const selectedCount = $derived.by(() => {
		if (!allMatchedIds) return addedIds.size;
		const base = Math.max(0, allMatchedIds.size - excludedIds.size);
		// Added rows that are not in the baseline (a book matched by filter
		// but missing from the capped ID list, checked individually).
		let extra = 0;
		for (const id of addedIds) if (!allMatchedIds.has(id)) extra++;
		return base + extra;
	});

	// The select-all checkbox's visible state: checked when every matching
	// book is selected (including off-page books), indeterminate otherwise.
	const selectAllChecked = $derived(
		response !== null && response.total > 0 && selectedCount === response.total
	);
	const selectAllIndeterminate = $derived(selectedCount > 0 && !selectAllChecked);
	// While a select-all request is in flight, the derived state above has
	// not moved yet; keep the tick on the side the operator clicked so a
	// slow response does not visibly revert the checkbox.
	let selectAllOptimistic = $state<boolean | null>(null);
	const selectAllBoxChecked = $derived(
		selectingAll && selectAllOptimistic !== null ? selectAllOptimistic : selectAllChecked
	);
	// The selector's summary row (profile picked, or the list unavailable)
	// is what breaks the control row onto two lines; the bar's alignment
	// switches on the same condition.
	const profileSummaryVisible = $derived(selectedProfileId !== null || profilesError !== null);

	$effect(() => {
		if (selectAllControl) {
			// While a select-all request is in flight the tick shows the
			// optimistic state; a stale partial selection must not dash it.
			selectAllControl.indeterminate = !selectingAll && selectAllIndeterminate;
		}
	});

	// Toolbar select-all control: checked → the whole matching set;
	// unchecked with a partial selection → deselect everything; unchecked
	// with none selected → select all again (tri-state).
	function onselectAllChange(event: Event) {
		const checked = (event.currentTarget as HTMLInputElement).checked;
		bulkError = null;
		if (checked) {
			selectAllOptimistic = true;
			if (!allMatchedIds) void selectAllMatching();
			else {
				excludedIds = new Set();
				addedIds = new Set();
				selectAllOptimistic = null;
			}
		} else if (selectedCount > 0) {
			deselectAllMatching();
			excludedIds = new Set();
			addedIds = new Set();
			selectAllOptimistic = null;
		} else {
			selectAllOptimistic = true;
			void selectAllMatching();
		}
	}

	// The book IDs the bulk requeue will send — the effective selection.
	function effectiveSelectedIds(): number[] {
		const ids = new Set<number>();
		if (allMatchedIds) {
			for (const id of allMatchedIds) if (!excludedIds.has(id)) ids.add(id);
		}
		for (const id of addedIds) ids.add(id);
		return [...ids];
	}

	function showBulkSuccess(message: string) {
		bulkSuccess = message;
		if (successTimer) clearTimeout(successTimer);
		successTimer = setTimeout(() => (bulkSuccess = null), 4000);
	}
	$effect(() => () => {
		if (successTimer) clearTimeout(successTimer);
	});

	// A filter/search change is a stale-selection signal for EVERY bulk
	// operation, so all of them share this early-exit pattern: a request that
	// outlives the criteria that created it is discarded, never applied.
	function criteriaChanged(requestedFilter: string, requestedQ: string) {
		return filter !== requestedFilter || q !== requestedQ;
	}

	async function bulkRequeue() {
		if (selectedCount === 0) return;
		bulkActing = 'requeue';
		bulkError = null;
		bulkSuccess = null;
		try {
			const ids = effectiveSelectedIds();
			// Snapshot with the ids: clearSelection() (filter/search change)
			// nulls selectedProfileId mid-await, which would drop the pick.
			const profileId = selectedProfileId;
			// One POST whose src_dirs are parallel to book_ids: each book
			// requeues from its own recorded source path, fetched per book.
			const books = await Promise.all(
				ids.map((id) => get<BookWithPeople>(`/books/${id}`)),
			);
			const payload: { book_ids: number[]; src_dirs: string[]; output_profile_id?: number } = {
				book_ids: ids,
				src_dirs: books.map((b) => b.src_path),
			};
			if (profileId !== null) payload.output_profile_id = profileId;
			await post(`/process`, payload);
			selectedProfileId = null; // Batch queued with this choice; back to Global settings.
			await goto('/process');
		} catch (e) {
			bulkError = toCleanedError(e);
			bulkErrorAction = 'select';
		} finally {
			bulkActing = null;
		}
	}

	// Mark the selection done: one PUT per book, sequential so a mid-way
	// failure leaves a coherent "n of m" and the survivors selected.
	async function bulkMarkDone() {
		const ids = effectiveSelectedIds();
		if (ids.length === 0) return;
		bulkActing = 'done';
		bulkError = null;
		bulkSuccess = null;
		bulkProgress = 0;
		const requestedFilter = filter;
		const requestedQ = q;
		let failed: CleanedAdbError | null = null;
		let done = 0;
		for (const id of ids) {
			try {
				await put(`/books/${id}`, { status: 'done' });
				done++;
			} catch (e) {
				failed = toCleanedError(e);
				break;
			}
			bulkProgress = done;
			// Drop the finished books from the live view as they land so the
			// counts and washes move one row at a time.
			pruneSelectionId(id);
		}
		if (criteriaChanged(requestedFilter, requestedQ)) return;
		if (failed) {
			bulkError = failed;
			bulkErrorAction = 'done';
		} else {
			showBulkSuccess(`${done} book${done === 1 ? '' : 's'} marked as done.`);
			// Nothing matching is left in the selection — collapse the bar.
			if (selectedCount === 0) clearSelection();
		}
		bulkActing = null;
	}

	// Removal is confirm-gated: open the dialog, then run it on confirm.
	async function openRemoveConfirm() {
		const ids = effectiveSelectedIds();
		if (ids.length === 0) return;
		confirmRemoveOpen = true;
		removeListLoading = true;
		removeList = [];
		try {
			// Titles for the confirmation list; the delete below uses the ids.
			// Rows already on the current page come from the list response —
			// only off-page ids need a fetch, so a large selection cannot fan
			// out one request per book.
			const known = new Map(
				(response?.books ?? []).map((b) => [b.id, b] as [number, BookWithPeople]),
			);
			const missingIds = ids.filter((id) => !known.has(id));
			const missing = await Promise.all(
				missingIds.map((id) => get<BookWithPeople>(`/books/${id}`)),
			);
			for (const b of missing) known.set(b.id, b);
			removeList = ids.map((id) => known.get(id)!);
		} catch (e) {
			confirmRemoveOpen = false;
			bulkError = toCleanedError(e);
			bulkErrorAction = 'remove';
		} finally {
			removeListLoading = false;
		}
	}

	async function bulkRemove() {
		const ids = removeList.map((b) => b.id);
		if (ids.length === 0) return;
		bulkActing = 'remove';
		bulkError = null;
		bulkSuccess = null;
		bulkProgress = 0;
		const requestedFilter = filter;
		const requestedQ = q;
		let failed: CleanedAdbError | null = null;
		let done = 0;
		let survivors: BookWithPeople[] = [];
		for (const book of removeList) {
			try {
				await del(`/books/${book.id}`);
				done++;
			} catch (e) {
				failed = toCleanedError(e);
				// This book and every one after it stay selected and in the
				// list, so Retry re-runs exactly the survivors.
				survivors = removeList.slice(removeList.indexOf(book));
				break;
			}
			bulkProgress = done;
			pruneSelectionId(book.id);
		}
		confirmRemoveOpen = false;
		removeList = failed ? survivors : [];
		if (criteriaChanged(requestedFilter, requestedQ)) return;
		if (failed) {
			bulkError = failed;
			bulkErrorAction = 'remove';
		} else {
			showBulkSuccess(`${done} book${done === 1 ? '' : 's'} removed.`);
			if (selectedCount === 0) clearSelection();
		}
		bulkActing = null;
	}

	// Remove one id from every selection set the moment its operation
	// succeeds, so the bar count and row washes track the live selection.
	function pruneSelectionId(id: number) {
		if (allMatchedIds) {
			if (allMatchedIds.has(id)) allMatchedIds = new Set(allMatchedIds);
			excludedIds = new Set(excludedIds);
		}
		if (allMatchedIds?.has(id)) allMatchedIds.delete(id);
		excludedIds.delete(id);
		addedIds = new Set(addedIds);
		addedIds.delete(id);
		if (allMatchedIds) allMatchedTotal = Math.max(0, allMatchedTotal - 1);
	}

	// Pagination jump
	let jumpValue = $state('');
	function jumpToPage() {
		const n = Number(jumpValue);
		if (!Number.isInteger(n) || n < 1) return;
		setPage(n - 1);
		jumpValue = '';
	}

	// Keyboard: j/k page, '/' focus search. Ignore inputs and open modals.
	function onKeydown(e: KeyboardEvent) {
		const target = e.target as HTMLElement | null;
		if (target) {
			const tag = target.tagName;
			if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || tag === 'DIALOG') return;
			if (target.isContentEditable) return;
		}
		if (document.querySelector('dialog[open]')) return;
		if (e.metaKey || e.ctrlKey || e.altKey) return;
		if (e.key === 'j') {
			setPage(currentPage + 1);
		} else if (e.key === 'k') {
			setPage(currentPage - 1);
		} else if (e.key === '/') {
			e.preventDefault();
			searchInput?.focus();
		}
	}

	$effect(() => {
		const nextFilter = normalizedFilter(pageStore.url.searchParams.get('status'));
		const nextPage = normalizedPage(pageStore.url.searchParams.get('page'));
		const nextQ = pageStore.url.searchParams.get('q') ?? '';
		if (filter !== nextFilter) filter = nextFilter;
		if (currentPage !== nextPage) currentPage = nextPage;
		if (q !== nextQ) q = nextQ;
		// Mirror the search term into the input field when the URL changes from
		// outside (back/forward, deep link). Gate on focus: while the user is
		// typing (input focused) we never touch the field, which keeps the
		// in-progress value authoritative and avoids racing the debounce nav.
		if (searchInput && document.activeElement !== searchInput && inputValue !== nextQ) {
			inputValue = nextQ;
		}
	});

	$effect(() => {
		filter;
		currentPage;
		q;
		loadBooks();
	});

	const activeFilterLabel = $derived(filters.find((item) => item.value === filter)?.label ?? 'All');
	const totalPages = $derived(response ? Math.max(1, Math.ceil(response.total / limit)) : 1);
	const startIndex = $derived(response && response.total > 0 ? currentPage * limit + 1 : 0);
	const endIndex = $derived(response ? Math.min((currentPage + 1) * limit, response.total) : 0);
</script>

<svelte:head>
	<title>Books — Bragi Books</title>
</svelte:head>

<svelte:window onkeydown={onKeydown} />

<PageHeader
	title="Books"
	description="Every book in your library, with status filters and bulk requeue."
/>

{#if dl.error}
	<div class="mb-6">
		<Alert variant="error" onretry={loadBooks}>
			{dl.error.message}
			<ErrorDetail raw={dl.error.raw} needsSettings={dl.error.needsSettings} />
		</Alert>
	</div>
{/if}

<!-- ── Status filters ────────────────────────────────────────── -->
<div class="mb-6" role="group" aria-labelledby="catalog-filter-heading">
	<div class="mb-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
		<h2 id="catalog-filter-heading" class="text-sm font-semibold text-[var(--text)]">Filter</h2>
		<div class="flex flex-wrap items-center gap-2">
			<input
				bind:this={searchInput}
				bind:value={inputValue}
				type="search"
				placeholder="Title or series…"
				class="min-h-11 w-full sm:w-64"
				aria-label="Search catalog"
				oninput={onSearchInput}
			/>
			{#if response && response.total > 0}
				<label
					class="flex min-h-11 cursor-pointer items-center gap-2 rounded-sm border border-[var(--border)] bg-[var(--surface)] px-3 text-sm font-medium text-[var(--text)] transition-colors hover:bg-[var(--surface-hover)]"
					class:opacity-60={selectingAll}
				>
					<input
						bind:this={selectAllControl}
						type="checkbox"
						disabled={selectingAll}
						checked={selectAllBoxChecked}
						onchange={onselectAllChange}
						aria-label={selectingAll ? 'Selecting all matching books…' : `Select all ${response.total} matching books`}
					/>
					{selectingAll ? 'Selecting…' : `Select all ${response.total}`}
				</label>
			{/if}
		</div>
	</div>
	<div class="flex flex-wrap items-center gap-2">
		{#each filters as f}
			<button
				type="button"
				class="min-h-11 rounded-sm border border-[var(--border)] bg-[var(--bg-panel)] px-3 py-2 text-sm font-medium transition-colors hover:bg-[var(--surface-hover)] hover:text-[var(--text)]"
				class:border-[var(--border-strong)]={filter === f.value}
				class:bg-[var(--surface)]={filter === f.value}
				class:text-[var(--text)]={filter === f.value}
				class:text-[var(--text-muted)]={filter !== f.value}
				onclick={() => setFilter(f.value)}
				aria-pressed={filter === f.value ? 'true' : 'false'}
			>
				{f.label}
			</button>
		{/each}
		<p class="ml-auto hidden items-center gap-1.5 text-xs text-[var(--text-muted)] sm:flex">
			Shortcuts:
			<kbd class="rounded-sm border border-[var(--border)] bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[11px]">/</kbd>
			search
			<kbd class="rounded-sm border border-[var(--border)] bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[11px]">j</kbd>
			<kbd class="rounded-sm border border-[var(--border)] bg-[var(--bg-panel)] px-1.5 py-0.5 font-mono text-[11px]">k</kbd>
			pages
		</p>
	</div>

	<!-- ── Bulk action bar ───────────────────────────────────── -->
	{#if selectedCount > 0}
		<div class="panel mt-3 p-3" role="region" aria-label="Bulk actions" aria-live="polite">
			<div class="flex flex-wrap items-start gap-3">
				<!-- Shares the controls' top edge while the profile caption
				     renders (a centered count floats in a tall block); on the
				     single-line Global-settings state it centers on the row. -->
				<p class="w-full text-sm text-[var(--text)] sm:w-auto sm:mr-auto {profileSummaryVisible ? '' : 'sm:self-center'}">
					<span class="font-mono font-semibold">{selectedCount}</span> books selected
					{#if allMatchedTruncated}
						<span class="text-xs text-[var(--state-amber)]"
							>({selectedCount} of {allMatchedTotal} matching — server cap 1,000)</span
						>
					{/if}
				</p>
				<!-- One control row: the profile pick and the actions share a
				     common top edge, so the select and buttons stay on the same
				     line whether or not a profile summary renders below. -->
				<div class="w-full min-w-0 sm:ml-auto sm:w-auto sm:flex sm:gap-x-3 {profileSummaryVisible ? 'sm:items-start' : 'sm:items-center'}">
					<div class="w-full min-w-0 sm:w-72">
						<OutputProfileSelector
							{profiles}
							bind:selectedProfileId
							{profilesError}
							batchCount={selectedCount}
							batchScope="queue"
							selectId="bulk-output-profile-select"
							summaryId="bulk-profile-summary"
							labelClass="text-sm"
							selectClass="flex-1 min-h-11 border border-[var(--border)] bg-[var(--bg-panel)] px-2 text-sm text-[var(--text)]"
							summaryClass="w-full text-xs"
						/>
					</div>
					<div class="mt-2 grid w-full grid-cols-1 gap-2 sm:mt-0 sm:flex sm:w-auto sm:items-center">
						<Button loading={bulkActing === 'requeue'} disabled={bulkActing !== null} onclick={bulkRequeue}>
							Requeue &amp; view progress
						</Button>
						<Button variant="secondary" loading={bulkActing === 'done'} disabled={bulkActing !== null} onclick={bulkMarkDone}>
							{bulkActing === 'done' ? `Marking ${bulkProgress} of ${selectedCount}…` : 'Mark as done'}
						</Button>
						<Button variant="danger" loading={bulkActing === 'remove'} disabled={bulkActing !== null} onclick={openRemoveConfirm}>
							{bulkActing === 'remove' ? `Removing ${bulkProgress} of ${selectedCount}…` : 'Remove'}
						</Button>
						<Button variant="ghost" disabled={bulkActing !== null} onclick={clearSelection}>Clear</Button>
					</div>
					<!-- Screen readers: the per-item counter on the buttons is
					     not in its own live region, so mirror it here. -->
					{#if bulkActing === 'done' || bulkActing === 'remove'}
						<p
							class="sr-only"
							role="status"
							>{bulkActing === 'done' ? `Marking ${bulkProgress} of ${selectedCount}…` : `Removing ${bulkProgress} of ${selectedCount}…`}</p
						>
					{/if}
				</div>
			</div>
		</div>
	{/if}
	{#if bulkSuccess}
		<p class="mt-2 text-sm text-[var(--state-green)]" role="status">{bulkSuccess}</p>
	{/if}
	{#if bulkError}
		<div class="mt-3">
			<Alert variant="error" onretry={() => (bulkErrorAction === 'select' ? selectAllMatching() : bulkErrorAction === 'done' ? bulkMarkDone() : bulkRemove())}>
				{bulkError.message}
				<ErrorDetail raw={bulkError.raw} needsSettings={bulkError.needsSettings} />
			</Alert>
		</div>
	{/if}
</div>

<!-- ── Removal confirmation ──────────────────────────────────── -->
<Modal
	open={confirmRemoveOpen}
	title="Remove books"
	closeDisabled={bulkActing === 'remove'}
	beforeClose={() => bulkActing !== 'remove'}
>
	<p class="text-sm text-[var(--text-secondary)]">
		{removeList.length} book{removeList.length === 1 ? '' : 's'} will be removed from the library.
		Files on disk are not touched.
	</p>
	{#if removeListLoading}
		<p class="mt-3 text-sm text-[var(--text-muted)]">Loading the list…</p>
	{:else if removeList.length > 0}
		<ul class="mt-3 max-h-72 overflow-y-auto rounded-sm border border-[var(--border-subtle)]" aria-label="Books to remove">
			{#each removeList as book (book.id)}
				<li class="border-b border-[var(--border-subtle)] px-3 py-2 last:border-b-0">
					<p class="truncate text-sm" title={book.title}>{book.title}</p>
					<p class="truncate text-xs text-[var(--text-muted)]">
						{book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
						{#if book.series} · {book.series}{/if}
					</p>
				</li>
			{/each}
		</ul>
	{/if}
	{#snippet footer()}
		<div class="flex items-center gap-2">
			<Button variant="ghost" disabled={bulkActing === 'remove'} onclick={() => (confirmRemoveOpen = false)}>Cancel</Button>
			<Button variant="danger" loading={bulkActing === 'remove'} disabled={removeListLoading || removeList.length === 0} onclick={bulkRemove}>
				Remove {removeList.length || selectedCount} {removeList.length === 1 ? 'book' : 'books'}
			</Button>
		</div>
	{/snippet}
</Modal>

<!-- ── Catalog list ──────────────────────────────────────────── -->
{#if response && !dl.showSkeleton}
	<p class="sr-only" role="status" aria-live="polite">
		{response.total === 0
			? `No ${activeFilterLabel.toLowerCase()} books found.`
			: `Showing ${startIndex} to ${endIndex} of ${response.total} ${activeFilterLabel.toLowerCase()} books.`}
	</p>
{/if}
{#if !dl.hasLoaded && !dl.showSkeleton}
	<!-- 200 ms delay window: announce pending, sized like the settled rows. -->
	<div
		class="flex h-[23.5rem] items-center justify-center overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]"
		role="status"
		aria-live="polite"
		aria-label="Catalog loading"
	>
		<span class="sr-only">Loading the book catalog…</span>
	</div>
{:else if dl.showSkeleton}
	<div class="overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]" aria-label="Catalog loading">
		{#each Array(5) as _}
			<div class="flex items-center gap-4 border-b border-[var(--border-subtle)] px-4 py-3 last:border-b-0">
				<Skeleton variant="rect" width="48px" height="48px" />
				<div class="flex-1">
					<Skeleton height="1rem" width="35%" />
					<Skeleton class="mt-2" height="0.875rem" width="20%" />
				</div>
			</div>
		{/each}
	</div>
{:else if dl.error && (!response || response.books.length === 0)}
	<!-- The failure alert above owns this space; do not also claim "empty". -->
{:else if !response || response.books.length === 0}
	<EmptyState
		title="No books found"
		description={filter === 'all' ? 'Import some source directories to build your library.' : `No ${filter} books right now.`}
		actionLabel="Import books"
		actionHref="/import"
	/>
{:else}
	<!-- Catalog list — whole row links to the book record -->
	<ul class="catalog-list overflow-hidden rounded-md border border-[var(--border)] bg-[var(--bg-panel)]" aria-label="Book catalog">
		{#each response.books as book (book.id)}
			<li>
				<div class="book-row" class:book-row--selected={isBookSelected(book)}>
					<label class="book-check">
						<input
							type="checkbox"
							checked={isBookSelected(book)}
							onchange={() => toggleBook(book)}
							aria-label="Select {book.title}"
						/>
					</label>
					<a href="/books/{book.id}" class="book-row__link" aria-label="{book.title} — open record">
						<CoverArt url={book.cover_image_url} title={book.title} size="sm" />
						<div class="min-w-0 flex-1">
							<h3 class="line-clamp-2 sm:truncate font-medium" title={book.title}>{book.title}</h3>
							<p
								class="truncate text-sm text-[var(--text-muted)]"
								title={book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
							>
								{book.authors.map((a) => a.name).join(', ') || 'Unknown author'}
								{#if book.series}
									<span class="text-[var(--text-muted)]"> · {book.series}</span>
								{/if}
								{#if book.runtime_length_minutes}
									<span class="text-[var(--text-secondary)]"> · {formatRuntime(book.runtime_length_minutes)}</span>
								{/if}
							</p>
							{#if book.status_message}
								<p class="mt-0.5 truncate text-xs text-[var(--text-muted)]">{book.status_message}</p>
							{/if}
						</div>
					</a>
					<StatusBadge status={book.status} />
				</div>
			</li>
		{/each}
	</ul>

	<!-- ── Pagination ────────────────────────────────────────── -->
	{#if totalPages > 1}
		<div class="mt-6 flex flex-wrap flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
			<p class="text-sm text-[var(--text-muted)]">
				Showing <span class="font-mono">{startIndex}–{endIndex}</span> of
				<span class="font-mono">{response.total}</span>
			</p>
			<div class="flex flex-wrap items-center gap-2">
				<Button variant="secondary" disabled={currentPage === 0} onclick={() => setPage(currentPage - 1)}>
					← Previous
				</Button>
				<span class="font-mono text-xs text-[var(--text-muted)]">
					Page {currentPage + 1} of {totalPages}
				</span>
				<Button variant="secondary" disabled={currentPage >= totalPages - 1} onclick={() => setPage(currentPage + 1)}>
					Next →
				</Button>
				<div class="ml-2 flex items-center gap-1">
					<input
						bind:value={jumpValue}
						type="number"
						min={1}
						max={totalPages}
						aria-label="Jump to page"
						class="w-16 font-mono text-sm"
						onkeydown={(e) => {
							if (e.key === 'Enter') jumpToPage();
						}}
					/>
					<Button variant="secondary" onclick={jumpToPage}>Go</Button>
				</div>
			</div>
		</div>
	{/if}
{/if}

<style>
	.catalog-list {
		display: flex;
		flex-direction: column;
	}
	.catalog-list li + li .book-row {
		border-top: 1px solid var(--border-subtle);
	}
	.book-row {
		display: grid;
		grid-template-columns: 2.75rem minmax(0, 1fr) auto;
		align-items: center;
		gap: 0.5rem 1rem;
		padding: 0.75rem 1rem 0.75rem 0.5rem;
	}
	.book-row:hover,
	.book-row:hover .book-row__link {
		background: var(--surface-hover);
	}
	.book-row--selected,
	.book-row--selected:hover,
	.book-row--selected:hover .book-row__link {
		background: var(--workspace-selected);
	}
	.book-check {
		display: flex;
		align-items: center;
		justify-content: center;
		min-height: 2.75rem;
		min-width: 2.75rem;
		cursor: pointer;
	}
	.book-row__link {
		display: flex;
		align-items: center;
		gap: 1rem;
		min-width: 0;
		text-decoration: none;
		color: inherit;
	}
</style>
