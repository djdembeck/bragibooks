<script lang="ts">
	import { tick } from 'svelte';
	import { get, put, post, authHeaders } from '#lib/api.js';
	import {
		type BookMetadataResponse,
		type BookMetadataDraft,
		type BookMetadataSnapshot,
		type CoverUploadResponse,
		type EnginePreviewResponse,
		type SnapshotChapter,
	} from '#lib/types.js';
	import { cleanAdbError, toCleanedError, type CleanedAdbError } from '#lib/adbError.js';
	import { Button, Alert, CoverArt, ErrorDetail, AbdTag } from '#lib/components/index.js';
	import { Trash2, ChevronUp, ChevronDown } from '@lucide/svelte';

	interface Props {
		bookId: number;
		/** Global path format for the naming preview; null while loading. */
		pathFormat: string | null;
		/** The parent's <details> shell open state: the Ctrl/Cmd+S save
		 *  shortcut is page-wide, so it must fire only while the editor
		 *  is on screen. */
		open?: boolean;
		/** Set when a save completed, so the caller can note the state. */
		onSaved?: () => void;
	}

	let { bookId, pathFormat, open = false, onSaved }: Props = $props();

	/* ── State ───────────────────────────────────────────────────
	 * The editor works on the server-computed override draft: every
	 * field carries the full tri-state (no override / edited value /
	 * cleared tag) exactly as the API defines it, so a reload shows
	 * the persisted state and nothing is re-derived locally.  */
	let meta = $state<BookMetadataResponse | null>(null);
	let loadError = $state<CleanedAdbError | null>(null);
	let draft = $state<BookMetadataDraft | null>(null);
	let saveError = $state<CleanedAdbError | null>(null);
	let saving = $state(false);
	let confirmingReset = $state(false);
	let confirmingDiscard = $state(false);
	let resetting = $state(false);
	/** Snapshot of the last loaded/saved draft; drives the saved status
	 *  and the partial-save payload (F5: only fields that differ from
	 *  this baseline are sent as edits; the rest are zero values). */
	let baseline = $state<BookMetadataDraft | null>(null);
	/** Cover options start collapsed unless the book already has a custom cover. */
	let coverOpen = $state(false);
	let chaptersExpanded = $state(false);
	let chapterOffset = $state('');
	let draggingChapter = $state<number | null>(null);
	let dropChapter = $state<number | null>(null);

	let coverInput = $state<HTMLInputElement | null>(null);
	let genreInput = $state<HTMLInputElement | null>(null);
	let uploading = $state(false);
	let chapterTimeError = $state('');

	// Debounced engine naming preview of the DRAFT (what the next job
	// would name the output from) — the engine owns the resolver.
	let previewPath = $state('');
	let previewError = $state(false);
	let previewSeq = 0;

	// Cover preview: the static `/covers/...` URL for file/snapshot covers,
	// or a blob URL for URL covers (fetched server-side below — an <img>
	// cannot POST the CSRF token, and the URL must stay out of the request
	// target). null = placeholder.
	let coverPreviewUrl = $state<string | null>(null);
	let coverPreviewSeq = 0;

	// Fetch generation (finding: SvelteKit reuses this component when
	// navigating /books/1 -> /books/2, and a draft loaded for book A must
	// never be submitted for book B — every save/upload URL is built from
	// the LIVE bookId). A plain, deliberately non-reactive counter: the
	// fetch effect re-runs on the bookId prop change and via the reload()
	// token below; each run mints a generation token, and the .then/.catch
	// guard against the LIVE token, so a response that outlived a
	// navigation (or a reset) is dropped instead of populating the wrong
	// book's state.
	let bookFetchSeq = 0;
	let reloadToken = $state(0);
	$effect(() => {
		bookId; // navigation dependency (state is wiped on every run)
		reloadToken; // explicit-reload dependency (same id)
		const gen = ++bookFetchSeq;
		const id = bookId;
		previewSeq++;
		previewPath = '';
		loadError = null;
		// A save failure on the previous book must not show in this book's
		// toolbar; an unapplied chapter offset must not be applied to (and
		// saved with) the new book.
		saveError = null;
		chapterOffset = '';
		meta = null;
		draft = null;
		baseline = null;
		coverPreviewUrl = null;
		coverOpen = false;
		confirmingReset = false;
		confirmingDiscard = false;
		chapterTimeError = '';
		// A stale upload from the previous book cannot clear its own
		// `finally` (the generation guard skips it); a new generation owns
		// a fresh flag so the new book never inherits a stuck loading state.
		uploading = false;
		// The same contract covers save and reset: their generation guards
		// skip the `finally` clear once the book has changed, so the new
		// book never inherits a stuck saving/resetting flag.
		saving = false;
		resetting = false;
		get<BookMetadataResponse>(`/books/${id}/metadata`)
			.then((resp) => {
				if (gen !== bookFetchSeq) return;
				meta = resp;
				const next = draftFrom(resp);
				draft = next;
				baseline = snapshotOf(next);
				coverOpen = next.cover_source !== '' || next.cleared.includes('cover');
			})
			.catch((e) => {
				if (gen !== bookFetchSeq) return;
				loadError = toCleanedError(e);
			});
	});

	// Reload the current book (used after a reset, which deletes the
	// override row without touching bookId): a new generation for the
	// SAME id.
	function reload() {
		reloadToken++;
	}

	function draftFrom(resp: BookMetadataResponse): BookMetadataDraft {
		const ov = resp.overrides;
		// Scalars, lists and year prefill from the server-computed
		// EFFECTIVE state (catalog with the override layer applied): the
		// stored row's "no override" zero values ("" / null) are not
		// blanks, so an override row with an empty scalar must not pin
		// the field to blank over the catalog value. The snapshot is the
		// fallback when there is no capture (effective is null then),
		// and the OVERRIDE ROW the last resort: an update can delete the
		// snapshot row while a stored override survives, and blanking
		// the prefill here would make the next save replace the hidden
		// stored lists with the empty ones. The override row stores the
		// same scalar/list fields, so it composes as a source.
		// Cleared / cover / chapters keep the OVERRIDE's tri-state: the
		// effective state cannot express "no override" for them.
		const source = resp.effective ?? resp.snapshot ?? ov;
		return {
			title: source?.title ?? '',
			subtitle: source?.subtitle ?? '',
			authors: [...(source?.authors ?? [])],
			narrators: [...(source?.narrators ?? [])],
			series_name: source?.series_name ?? '',
			series_position: source?.series_position ?? '',
			description: source?.description ?? '',
			genres: [...(source?.genres ?? [])],
			year: source?.year ?? null,
			cleared: ov?.cleared ?? [],
			cover_source: ov?.cover_source ?? '',
			cover_file: ov?.cover_file ?? '',
			cover_url: ov?.cover_url ?? '',
			chapters: ov?.chapters ?? null
		};
	}

	/** Deep copy of a draft for stable comparison and the partial-save
	 *  baseline. The cleared list is sorted so it is order-insensitive. */
	function snapshotOf(d: BookMetadataDraft): BookMetadataDraft {
		return {
			title: d.title,
			subtitle: d.subtitle,
			authors: d.authors ? [...d.authors] : null,
			narrators: d.narrators ? [...d.narrators] : null,
			series_name: d.series_name,
			series_position: d.series_position,
			description: d.description,
			genres: d.genres ? [...d.genres] : null,
			year: d.year,
			cleared: [...d.cleared].sort(),
			cover_source: d.cover_source,
			cover_file: d.cover_file,
			cover_url: d.cover_url,
			chapters: d.chapters ? d.chapters.map((c) => ({ ...c })) : null
		};
	}

	const draftEquals = (a: BookMetadataDraft, b: BookMetadataDraft) =>
		JSON.stringify(snapshotOf(a)) === JSON.stringify(snapshotOf(b));

	/** PUT body. A field that differs from the saved baseline carries the
	 *  draft value; an UNCHANGED field carries the stored row's value (the
	 *  zero value when there is no row). Sending the effective value for an
	 *  untouched field would pin the catalog value as an override, so a
	 *  re-capture would no longer update it — the tri-state must round-trip
	 *  the row, not the post-override state. */
	function savePayload(): BookMetadataDraft {
		const d = snapshotOf(draft!);
		const b = snapshotOf(baseline!);
		const ov = meta?.overrides;
		const keep = <K extends keyof BookMetadataDraft>(k: K, stored: BookMetadataDraft[K]) =>
			JSON.stringify(d[k]) === JSON.stringify(b[k]) ? stored : d[k];
		return {
			title: keep('title', ov?.title ?? ''),
			subtitle: keep('subtitle', ov?.subtitle ?? ''),
			authors: keep('authors', ov?.authors ?? null),
			narrators: keep('narrators', ov?.narrators ?? null),
			series_name: keep('series_name', ov?.series_name ?? ''),
			series_position: keep('series_position', ov?.series_position ?? ''),
			description: keep('description', ov?.description ?? ''),
			genres: keep('genres', ov?.genres ?? null),
			year: keep('year', ov?.year ?? null),
			cleared: keep('cleared', ov?.cleared ?? []),
			cover_source: keep('cover_source', ov?.cover_source ?? ''),
			cover_file: keep('cover_file', ov?.cover_file ?? ''),
			cover_url: keep('cover_url', ov?.cover_url ?? ''),
			chapters: keep('chapters', ov?.chapters ?? null)
		};
	}

	const snapshot = $derived(meta?.snapshot ?? null);
	const dirty = $derived(!!draft && !!baseline && !draftEquals(draft, baseline));
	const hasSavedOverrides = $derived(!!meta?.overrides);
	const statusText = $derived(
		saving
			? 'Saving…'
			: resetting
				? 'Resetting…'
				: dirty
					? 'Unsaved changes'
					: hasSavedOverrides
						? 'All changes saved'
						: 'No saved edits yet'
	);

	// The two ordered-people sections, typed so the edit helpers (which
	// take the literal union, not string) stay sound in the template.
	const peopleSections = $derived([
		{ key: 'authors', label: 'Authors' },
		{ key: 'narrators', label: 'Narrators' }
	] as const);

	function personNoun(label: string): string {
		return label === 'Authors' ? 'author' : 'narrator';
	}

	// ── Cleared / edited indicators (server state, not a local guess) ──
	function isCleared(field: string): boolean {
		return draft?.cleared.includes(field) ?? false;
	}

	function setClear(field: string, cleared: boolean) {
		if (!draft) return;
		if (cleared === isCleared(field)) return;
		draft.cleared = cleared
			? [...draft.cleared.filter((f) => f !== field), field]
			: draft.cleared.filter((f) => f !== field);
	}

	function removeAllChapters() {
		if (!draft) return;
		draft.chapters = [];
		setClear('chapters', true);
	}

	const isEditedChapters = $derived(!!draft && draft.chapters !== null);
	const chapterList = $derived(draft?.chapters ?? snapshot?.chapters ?? []);
	const visibleChapters = $derived(
		chaptersExpanded ? chapterList : chapterList.slice(0, 5)
	);
	const chaptersCleared = $derived(
		!!draft && (isCleared('chapters') || (draft.chapters !== null && draft.chapters.length === 0))
	);

	const tagFields = [
		'title',
		'subtitle',
		'authors',
		'narrators',
		'series',
		'description',
		'genres',
		'year',
		'chapters'
	] as const;
	const tagFieldLabels: Record<(typeof tagFields)[number], string> = {
		title: 'Title',
		subtitle: 'Subtitle',
		authors: 'Authors',
		narrators: 'Narrators',
		series: 'Series',
		description: 'Description',
		genres: 'Genres',
		year: 'Year',
		chapters: 'Chapters'
	};
	const omittedFields = $derived(
		tagFields.filter(
			(field) => isCleared(field) || (field === 'chapters' && chaptersCleared)
		)
	);
	const tagRemovalCount = $derived(omittedFields.length);
	const omittedLabel = $derived(
		omittedFields.length
			? omittedFields.map((field) => tagFieldLabels[field]).join(', ')
			: 'None omitted'
	);

	// ── Effective draft → engine naming preview ──────────────────
	// Mirrors the server's applyMetadataOverride effective state for NAMING
	// purposes: edited value wins, catalog value otherwise, nothing else
	// changes. A cleared field is intentionally NOT blanked here: the
	// server no longer blanks the effective value for a clear (the engine
	// "clear" policy removes the tag atom at run time, while the effective
	// value keeps naming the output), so the preview payload must carry the
	// value the frozen job will name from.
	// The payload is a $derived so it is recomputed synchronously from the
	// CURRENT draft fields (the effect below reads only this derived, which
	// is what tracks every nested edit — reading fields inside the delayed
	// callback would not retrigger the preview after the first request).
	// Cleared fields are intentionally NOT blanked here: the server no
	// longer blanks the effective value for a clear (the engine "clear"
	// policy removes the tag atom at run time, while the effective value
	// keeps naming the output), so the payload carries the value the
	// frozen job will name from — the catalog value for a cleared list,
	// the edited value for a cleared scalar.
	const previewMetadata = $derived.by((): Record<string, unknown> => {
		const d = draft;
		if (!d) return {};
		const metaPayload: Record<string, unknown> = {
			title: d.title || (snapshot?.title ?? ''),
			authors: d.authors ?? (snapshot?.authors ?? []),
			narrators: d.narrators ?? (snapshot?.narrators ?? [])
		};
		const subtitle = d.subtitle || (snapshot?.subtitle ?? '');
		if (subtitle) metaPayload.subtitle = subtitle;
		const seriesName = d.series_name || (snapshot?.series_name ?? '');
		if (seriesName) metaPayload.series_name = seriesName;
		const seriesPosition = d.series_position || (snapshot?.series_position ?? '');
		if (seriesPosition) metaPayload.series_position = seriesPosition;
		const description = d.description || (snapshot?.description ?? '');
		if (description) metaPayload.description = description;
		const genres = d.genres ?? (snapshot?.genres ?? []);
		if (genres.length) metaPayload.genres = genres;
		const year = d.year ?? snapshot?.year ?? null;
		if (year) metaPayload.year = year;
		if (snapshot?.audiobookdb_book_id) metaPayload.book_id = snapshot.audiobookdb_book_id;
		return metaPayload;
	});

	const previewTitle = $derived((draft?.title || snapshot?.title || '').trim());

	$effect(() => {
		const fmt = pathFormat;
		const payload = previewMetadata;
		if (!draft || !fmt) return;
		// The engine rejects an empty effective title ("effective title is
		// empty"), so a payload with none can only produce a 502 — for a
		// book whose catalog title is blank (rare) there is nothing to
		// preview: skip instead of posting a guaranteed failure.
		if (!String(payload.title ?? '').trim()) return;
		const seq = ++previewSeq;
		previewError = false;
		const timer = setTimeout(async () => {
			try {
				const resp = await post<EnginePreviewResponse>('/engine/preview', {
					metadata: payload,
					path_format: fmt
				});
				if (seq === previewSeq) {
					previewPath = resp.relative_path;
					previewError = false;
				}
			} catch {
				if (seq === previewSeq) {
					previewPath = '';
					previewError = true;
				}
			}
		}, 250);
		return () => clearTimeout(timer);
	});

	// ── Cover handling ───────────────────────────────────────────
	// Static cover sources (uploaded file, snapshot art) render straight
	// from the covers directory.
	const coverPreviewStatic = $derived.by<string | null>(() => {
		if (!draft) return null;
		if (draft.cover_source === 'file' && draft.cover_file) return `/covers/${draft.cover_file}`;
		if (draft.cover_source === 'url') return null; // fetched below
		if (draft.cover_source === 'clear' || isCleared('cover')) return null;
		return snapshot?.cover_file ? `/covers/${snapshot.cover_file}` : null;
	});

	// A URL cover is previewed through the server's validated fetch path,
	// not loaded directly by the browser: a raw external URL as an <img>
	// src is resolved and followed by the browser independently of the
	// server's public-destination and redirect checks, so a saved URL
	// could make this operator's browser contact a destination reachable
	// from their network. The endpoint re-asserts the same guard on every
	// hop and serves the fetched bytes. The URL travels in the POST body
	// (never the query string — a draft URL can carry credentials in its
	// path or a signed query, and query strings end up in proxy
	// request-target logs), and the POST puts the fetch under the CSRF
	// guard (an <img> load is a GET and would be exempt). The response
	// becomes a blob URL the <img> can load same-origin.
	//
	// Blob URLs are tracked per effect run and revoked in the cleanup:
	// the cleanup runs when the effect re-runs (a new preview replaces the
	// old, or a book reset/draft change wipes the state) and on unmount,
	// so a navigation that clears coverPreviewUrl cannot strand the
	// previous book's blob for the page's lifetime.
	$effect(() => {
		const staticUrl = coverPreviewStatic; // non-URL covers: depend on the static path
		const draftUrl = draft?.cover_source === 'url' ? (draft?.cover_url ?? '') : '';
		const seq = ++coverPreviewSeq;
		let activeBlob: string | null = null;
		const revoke = () => {
			if (activeBlob) {
				URL.revokeObjectURL(activeBlob);
				activeBlob = null;
			}
		};
		if (!draftUrl) {
			coverPreviewUrl = staticUrl;
			return revoke;
		}
		fetch('/covers/preview', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json', ...authHeaders() },
			body: JSON.stringify({ url: draftUrl })
		})
			.then(async (res) => {
				if (seq !== coverPreviewSeq) return;
				if (!res.ok) throw new Error(await res.text());
				const blob = await res.blob();
				if (seq !== coverPreviewSeq) return;
				activeBlob = URL.createObjectURL(blob);
				coverPreviewUrl = activeBlob;
			})
			.catch(() => {
				if (seq === coverPreviewSeq) {
					coverPreviewUrl = null;
				}
			});
		return revoke;
	});

	const coverSource = $derived(draft?.cover_source || (isCleared('cover') ? 'clear' : ''));
	const coverSummary = $derived.by(() => {
		switch (coverSource) {
			case 'file':
				return 'Uploaded artwork';
			case 'url':
				return 'Cover from URL';
			case 'clear':
				return 'No cover in output';
			default:
				return snapshot?.cover_file ? 'Catalog cover' : 'No catalog cover';
		}
	});

	async function onCoverFile(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		input.value = '';
		if (!file || !draft) return;
		uploading = true;
		saveError = null;
		// Capture the book + generation the upload started for: if the user
		// navigates (bookId changes, bookFetchSeq bumps) while the request
		// is in flight, the response must not be applied to the NEW book's
		// draft (it would set a cover file owned by the old book, which the
		// server rejects on save).
		const startBook = bookId;
		const startSeq = bookFetchSeq;
		try {
			const form = new FormData();
			form.append('file', file);
			const res = await fetch(`/api/books/${bookId}/metadata/cover`, {
				method: 'POST',
				headers: { 'X-CSRF-Token': csrfToken() },
				body: form
			});
			if (!res.ok) {
				throw new Error((await res.text()) || `upload failed (${res.status})`);
			}
			const resp = (await res.json()) as CoverUploadResponse;
			if (bookId !== startBook || bookFetchSeq !== startSeq || !draft) return;
			draft.cover_source = 'file';
			draft.cover_file = resp.cover_file;
			draft.cover_url = '';
			draft.cleared = draft.cleared.filter((f) => f !== 'cover');
			coverOpen = true;
		} catch (err) {
			if (bookId === startBook && bookFetchSeq === startSeq) {
				saveError = cleanAdbError(String(err));
			}
		} finally {
			if (bookId === startBook && bookFetchSeq === startSeq) {
				uploading = false;
			}
		}
	}

	function csrfToken(): string {
		try {
			return (
				document.cookie
					.split('; ')
					.find((c) => c.startsWith('bragibooks_csrf='))
					?.slice('bragibooks_csrf='.length) ?? ''
			);
		} catch {
			return '';
		}
	}

	function setCoverSource(source: BookMetadataDraft['cover_source']) {
		if (!draft) return;
		draft.cover_source = source;
		draft.cover_file = '';
		draft.cover_url = '';
		if (source !== 'clear') draft.cleared = draft.cleared.filter((f) => f !== 'cover');
	}

	// ── People editing (ordered lists) ───────────────────────────
	function ensureList(key: 'authors' | 'narrators'): string[] {
		if (!draft) return [];
		if (draft[key] === null) draft[key] = snapshot ? [...(snapshot[key] ?? [])] : [];
		return draft[key] as string[];
	}

	function movePerson(key: 'authors' | 'narrators', index: number, dir: -1 | 1) {
		if (!draft) return;
		const list = ensureList(key);
		const target = index + dir;
		if (target < 0 || target >= list.length) return;
		const [item] = list.splice(index, 1);
		list.splice(target, 0, item);
		draft[key] = [...list];
	}

	function removePerson(key: 'authors' | 'narrators', index: number) {
		if (!draft) return;
		const list = ensureList(key);
		list.splice(index, 1);
		draft[key] = [...list];
	}

	function addPerson(key: 'authors' | 'narrators') {
		if (!draft) return;
		const list = ensureList(key);
		list.push('');
		draft[key] = [...list];
	}

	// ── Genre editing (ordered chip list) ────────────────────────
	function ensureGenres(): string[] {
		if (!draft) return [];
		if (draft.genres === null) draft.genres = [...(snapshot?.genres ?? [])];
		return draft.genres as string[];
	}

	function addGenre(e: KeyboardEvent) {
		if (e.key !== 'Enter' && e.key !== ',') return;
		e.preventDefault();
		commitGenreInput(e.currentTarget as HTMLInputElement);
	}

	function commitGenreInput(target: HTMLInputElement) {
		const value = target.value.trim().replace(/,+$/, '').trim();
		if (!value) return;
		const list = ensureGenres();
		if (!list.includes(value)) list.push(value);
		draft!.genres = [...list];
		target.value = '';
	}

	function removeGenre(index: number) {
		if (!draft) return;
		const list = ensureGenres();
		list.splice(index, 1);
		draft.genres = [...list];
	}

	// ── Chapter editing ──────────────────────────────────────────
	function ensureChapters(): SnapshotChapter[] {
		if (!draft) return [];
		if (draft.chapters === null) draft.chapters = [...(snapshot?.chapters ?? [])];
		return draft.chapters as SnapshotChapter[];
	}

	function addChapter() {
		if (!draft) return;
		const list = ensureChapters();
		const lastEnd = list.length
			? list[list.length - 1].start_millis + list[list.length - 1].duration_millis
			: 0;
		list.push({ title: '', start_millis: lastEnd, duration_millis: 0 });
		draft.chapters = [...list];
		chaptersExpanded = true;
	}

	function removeChapter(index: number) {
		if (!draft) return;
		const list = ensureChapters();
		list.splice(index, 1);
		draft.chapters = [...list];
	}

	function moveChapter(index: number, dir: -1 | 1) {
		if (!draft) return;
		const list = ensureChapters();
		const target = index + dir;
		if (target < 0 || target >= list.length) return;
		if (!chaptersExpanded && target >= 5) chaptersExpanded = true;
		const [item] = list.splice(index, 1);
		list.splice(target, 0, item);
		draft.chapters = [...list];
	}

	function moveChapterTo(from: number, to: number) {
		if (!draft || from === to) return;
		const list = ensureChapters();
		if (from < 0 || to < 0 || from >= list.length || to >= list.length) return;
		if (!chaptersExpanded && to >= 5) chaptersExpanded = true;
		const [item] = list.splice(from, 1);
		list.splice(to, 0, item);
		draft.chapters = [...list];
	}

	async function handleChapterKeydown(event: KeyboardEvent, index: number) {
		if (!event.altKey || (event.key !== 'ArrowUp' && event.key !== 'ArrowDown')) return;
		event.preventDefault();
		const target = index + (event.key === 'ArrowUp' ? -1 : 1);
		if (target < 0 || target >= chapterList.length) return;
		moveChapter(index, event.key === 'ArrowUp' ? -1 : 1);
		await tick();
		document.querySelector<HTMLButtonElement>(`[data-chapter-grip="${target}"]`)?.focus();
	}

	// The people and chapter-timing inputs commit to the draft only on
	// change/blur (onchange), so a Ctrl/Cmd+S mid-typing would save a
	// stale draft (or the response rebuild would overwrite the typed
	// value). Flush the open input's value into the draft before the
	// dirty check and the save.
	function flushChangeOnlyInputs() {
		if (!draft) return;
		// Set when a chapter-time input holds an invalid value; after the
		// chapter loop, a chapter-time error from a PRIOR flush clears
		// once every input is valid again (see below the chapter loop).
		// A fresh invalid flush re-sets the error, so the clear only
		// applies to a stale one.
		let chapterTimeInvalid = false;
		for (const el of document.querySelectorAll<HTMLInputElement>('.meditor__input--person')) {
			const label = el.getAttribute('aria-label') ?? '';
			const m = label.match(/^(\w+) (\d+)$/);
			if (!m) continue;
			const noun = m[1];
			const idx = Number(m[2]) - 1;
			const key = noun === 'author' ? 'authors' : 'narrators';
			const list = [...(draft[key] ?? [])];
			if (idx >= 0 && idx < list.length && list[idx] !== el.value) {
				list[idx] = el.value;
				draft[key] = list;
			}
		}
		// The year input commits on change/blur like the person inputs, so a
		// Ctrl/Cmd+S mid-typing would save a stale draft (or the response
		// rebuild would overwrite the typed value). Flush it here too.
		const yearEl = document.querySelector<HTMLInputElement>('.meditor__input--year');
		if (yearEl && yearEl.value !== String(draft.year ?? '')) {
			draft.year = yearEl.value === '' ? null : Number(yearEl.value);
		}
		for (const el of document.querySelectorAll<HTMLInputElement>('.meditor__chapter-title, .meditor__chapter-start, .meditor__chapter-duration')) {
			const row = el.closest('.meditor__chapter');
			if (!row) continue;
			const rows = Array.from(row.parentElement?.querySelectorAll('.meditor__chapter') ?? []);
			const idx = rows.indexOf(row);
			if (idx < 0) continue;
			const displayed = chapterList[idx];
			if (!displayed) continue;
			// Only a value that ACTUALLY differs from what the draft
			// (or the catalog baseline, while draft.chapters is still
			// null) already represents is flushed. Materializing
			// draft.chapters for an untouched input would turn the null
			// baseline into a copied catalog list and pin the catalog
			// chapters as an override on the next save.
			if (el.classList.contains('meditor__chapter-title')) {
				if (el.value !== displayed.title) {
					const list = ensureChapters();
					list[idx] = { ...list[idx], title: el.value };
					draft.chapters = [...list];
				}
			} else {
				const field = el.classList.contains('meditor__chapter-start') ? 'start_millis' : 'duration_millis';
				// chapterTimeInvalid is sticky for the whole loop: a valid
				// (or unchanged) input never clears it, so an invalid
				// earlier input followed by a valid later one still
				// blocks the shortcut save and keeps the stale-error
				// clear from running on the unflushed invalid value.
				if (el.value !== formatChapterTime(displayed[field])) {
					const parsed = parseChapterTime(el.value);
					if (parsed === null) {
						// The onchange path's gate: an invalid time must
						// not be silently dropped from the shortcut
						// save — the shortcut is blocked on it below.
						chapterTimeInvalid = true;
						chapterTimeError = 'Use h:mm:ss or milliseconds for chapter times.';
						continue;
					}
					if (parsed !== displayed[field]) {
						const list = ensureChapters();
						list[idx] = { ...list[idx], [field]: parsed };
						draft.chapters = [...list];
					}
				}
			}
		}
		if (!chapterTimeInvalid &&
			chapterTimeError === 'Use h:mm:ss or milliseconds for chapter times.') {
			// Every pending chapter-time input is valid: an error set by
			// an EARLIER flush (the user since corrected the value
			// without blurring, so onchange never ran to clear it) would
			// otherwise keep refusing the shortcut save forever. Only the
			// chapter-time message clears here — the offset input owns a
			// distinct message and must survive the flush.
			chapterTimeError = '';
		}
	}

	function handleGlobalKeydown(event: KeyboardEvent) {
		if (event.isComposing) return;
		if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 's') return;
		// The listener is page-wide; the shortcut belongs to the editor,
		// so it must not swallow a keystroke while the shell is closed.
		if (!open) return;
		if (confirmingReset || confirmingDiscard) return;
		event.preventDefault();
		if (saving || uploading || resetting) return;
		flushChangeOnlyInputs();
		if (!dirty || chapterErrors.length > 0 || chapterTimeError) return;
		void save();
	}

	function useCatalogChapters() {
		if (!draft) return;
		draft.chapters = null;
		// A saved clear ("No chapters (clear tag)") must not survive the
		// operator choosing the catalog list again: the server only drops
		// the marker for a non-empty edited list, so the client strips it
		// here explicitly (finding: clear-tag stuck after returning to catalog).
		setClear('chapters', false);
	}

	function applyChapterOffset() {
		if (!draft) return;
		const offset = parseChapterOffset(chapterOffset);
		if (offset === null) {
			chapterTimeError = 'Use h:mm:ss or milliseconds for the offset.';
			return;
		}
		chapterTimeError = '';
		const list = ensureChapters();
		draft.chapters = list.map((c) => ({
			...c,
			start_millis: Math.max(0, c.start_millis + offset)
		}));
		chapterOffset = '';
	}

	/** Client-side chapter diagnostics, mirroring the server's validation
	 *  so the operator sees the problem before it round-trips. */
	const chapterErrors = $derived.by(() => {
		const problems: string[] = [];
		const list = draft?.chapters ?? null;
		if (list === null) return problems;
		list.forEach((c, i) => {
			const label = `Chapter ${i + 1}`;
			if (!c.title.trim()) problems.push(`${label}: the title is empty.`);
			if (c.start_millis < 0 || c.duration_millis < 0)
				problems.push(`${label}: times must be zero or more.`);
			if (c.duration_millis === 0) problems.push(`${label}: a zero duration removes no range.`);
		});
		let previousEnd = 0;
		for (let i = 0; i < (list?.length ?? 0); i++) {
			const c = list![i];
			if (c.start_millis < previousEnd)
				problems.push(`Chapter ${i + 1} starts before chapter ${i} ends — ranges must be ordered.`);
			previousEnd = c.start_millis + c.duration_millis;
		}
		return problems;
	});

	/** h:mm:ss(.ms) from whole milliseconds for the timing inputs. */
	function formatChapterTime(millis: number): string {
		const total = Math.max(0, Math.floor(millis));
		const fraction = total % 1000;
		const totalSeconds = Math.floor(total / 1000);
		const h = Math.floor(totalSeconds / 3600);
		const m = Math.floor((totalSeconds % 3600) / 60);
		const s = totalSeconds % 60;
		const base = h
			? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
			: `${m}:${String(s).padStart(2, '0')}`;
		return fraction ? `${base}.${String(fraction).padStart(3, '0')}` : base;
	}

	/** Accept h:mm:ss(.ms); bare digits are a raw-millisecond fallback. */
	function parseChapterTime(value: string): number | null {
		const raw = value.trim();
		if (!raw) return null;
		if (/^\d+$/.test(raw)) {
			const millis = Number(raw);
			return Number.isSafeInteger(millis) ? millis : null;
		}
		const match = raw.match(/^(?:(\d+):)?(\d{1,2}):(\d{1,2})(?:\.(\d{1,3}))?$/);
		if (!match) return null;
		const [, hoursText, minutesText, secondsText, fractionText] = match;
		const hours = hoursText ? Number(hoursText) : 0;
		const minutes = Number(minutesText);
		const seconds = Number(secondsText);
		if (minutes > 59 || seconds > 59) return null;
		const fraction = fractionText ? Number(fractionText.padEnd(3, '0')) : 0;
		// The same safe-integer check the raw-millisecond branch applies: an
		// oversized hours component (the regex allows any digit count) would
		// otherwise produce an unsafe integer — or Infinity — that JSON
		// serialization converts to null instead of rejecting the value
		// locally. The offset result inherits the check (sign * parsed).
		const millis = (hours * 3600 + minutes * 60 + seconds) * 1000 + fraction;
		return Number.isSafeInteger(millis) ? millis : null;
	}

	function parseChapterOffset(value: string): number | null {
		const raw = value.trim();
		if (!raw) return null;
		const sign = raw.startsWith('-') ? -1 : 1;
		const parsed = parseChapterTime(sign < 0 ? raw.slice(1) : raw);
		return parsed === null ? null : sign * parsed;
	}

	// ── Save / discard / reset ───────────────────────────────────
	/** Overlay the edits made AFTER a save was submitted onto the
	 *  response-based draft: a field is taken from the live draft only
	 *  when it differs from the pre-submission snapshot, so in-flight
	 *  edits (inputs stay enabled while saving) survive the save and
	 *  untouched fields take the server's returned state. Comparison
	 *  goes through snapshotOf, so cleared is sorted (order-insensitive)
	 *  and the chapters null vs [] tri-state compares verbatim. */
	function overlayPostSubmitEdits(
		base: BookMetadataDraft,
		live: BookMetadataDraft,
		submitted: BookMetadataDraft
	): BookMetadataDraft {
		const l = snapshotOf(live);
		const changed = <K extends keyof BookMetadataDraft>(k: K) =>
			JSON.stringify(l[k]) !== JSON.stringify(submitted[k]);
		return {
			title: changed('title') ? l.title : base.title,
			subtitle: changed('subtitle') ? l.subtitle : base.subtitle,
			authors: changed('authors') ? l.authors : base.authors,
			narrators: changed('narrators') ? l.narrators : base.narrators,
			series_name: changed('series_name') ? l.series_name : base.series_name,
			series_position: changed('series_position') ? l.series_position : base.series_position,
			description: changed('description') ? l.description : base.description,
			genres: changed('genres') ? l.genres : base.genres,
			year: changed('year') ? l.year : base.year,
			cleared: changed('cleared') ? l.cleared : base.cleared,
			cover_source: changed('cover_source') ? l.cover_source : base.cover_source,
			cover_file: changed('cover_file') ? l.cover_file : base.cover_file,
			cover_url: changed('cover_url') ? l.cover_url : base.cover_url,
			chapters: changed('chapters') ? l.chapters : base.chapters
		};
	}

	async function save() {
		if (!draft || saving || uploading || !dirty) return; // the server sweep races an in-flight upload
		if (chapterErrors.length) return; // diagnostics already visible
		// Capture the generation the save belongs to: SvelteKit reuses this
		// component when navigating /books/A -> /books/B, and a response
		// that outlives the navigation must not paint book A's result into
		// book B's state (meta, saved note) or fire A's onSaved on B.
		const startBook = bookId;
		const startGen = bookFetchSeq;
		saving = true;
		saveError = null;
		confirmingDiscard = false;
		try {
			// The inputs stay enabled while the PUT is in flight, so the
			// live draft can change mid-flight. The change-only inputs
			// (person, year, chapter title/time) commit to the draft only on
			// blur, so a value still being typed lives only in its input:
			// flush them into the draft NOW so the submitted snapshot carries
			// them and the post-submission overlay (which diffs live draft
			// against submitted) keeps them through the response rebuild —
			// without this, a response that changes the author/chapter row
			// the key or replaces the year value overwrites the typed edit.
			flushChangeOnlyInputs();
			// The flush validates chapter-time inputs: an invalid value that
			// was still unblurred is detected HERE (the pre-save check above
			// ran before the flush). Bail with the error visible — the
			// Ctrl/Cmd+S path already does this — so the old draft is not
			// submitted and the invalid value stays shown in its input.
			if (chapterTimeError) return;
			// Snapshot exactly what is about to be sent (savePayload
			// serializes the same snapshotOf(draft) view) and diff against
			// it after the save.
			const submitted = snapshotOf(draft);
			const resp = await put<BookMetadataResponse>(`/books/${startBook}/metadata`, savePayload());
			if (bookId !== startBook || bookFetchSeq !== startGen) return;
			// The response is the authoritative stored state: a first
			// save on a historical book creates a capture and returns
			// values the submitted draft never had (captured authors),
			// and the server can normalize a field (clearing the subtitle
			// override to '' restores the catalog subtitle). Rebuild the
			// draft from the response, and base the baseline on THAT
			// response-derived draft, before overlaying post-submission
			// edits. Invariant: the baseline is the server's returned
			// stored state; edits made after submission stay dirty on top
			// of it and are picked up by the next save. A baseline taken
			// from the overlaid result (or the pre-save live draft) would
			// mark in-flight edits as persisted and make a later save of
			// another field omit them for good.
			meta = resp;
			const responseDraft = draftFrom(resp);
			baseline = snapshotOf(responseDraft);
			draft = overlayPostSubmitEdits(responseDraft, draft, submitted);
			onSaved?.();
		} catch (e) {
			if (bookId !== startBook || bookFetchSeq !== startGen) return;
			saveError = toCleanedError(e);
		} finally {
			if (bookId === startBook && bookFetchSeq === startGen) saving = false;
		}
	}

	/** Return to the last loaded/saved values without touching the server. */
	function discardChanges() {
		if (!meta || !dirty || saving || resetting || uploading) return;
		const next = draftFrom(meta);
		draft = next;
		baseline = snapshotOf(next);
		confirmingDiscard = false;
		saveError = null;
		coverOpen = next.cover_source !== '' || next.cleared.includes('cover');
	}

	async function doReset() {
		if (resetting || uploading || saving || !meta?.overrides) return; // a pending save would resurrect its edits
		// Same generation guard as save: the component is reused across
		// /books/A -> /books/B, and a reset response that outlives the
		// navigation must not run its continuation on book B (clear B's
		// confirm state, reload B, fire B's onSaved) nor clear a resetting
		// flag B set.
		const startBook = bookId;
		const startGen = bookFetchSeq;
		resetting = true;
		saveError = null;
		try {
			await post(`/books/${startBook}/metadata/reset`, {});
			if (bookId !== startBook || bookFetchSeq !== startGen) return;
			confirmingReset = false;
			reload();
			onSaved?.();
		} catch (e) {
			if (bookId !== startBook || bookFetchSeq !== startGen) return;
			saveError = toCleanedError(e);
			confirmingReset = false;
		} finally {
			if (bookId === startBook && bookFetchSeq === startGen) resetting = false;
		}
	}
</script>

{#snippet tagPolicyRow(field: string, label: string, accessible: string)}
	<label class="meditor__tagpolicy-row">
		<input
			type="checkbox"
			checked={isCleared(field)}
			aria-label="Remove {accessible} from output"
			onchange={(e) => setClear(field, (e.currentTarget as HTMLInputElement).checked)}
		/>
		<span>{label}</span>
	</label>
{/snippet}

{#snippet outputPreview()}
	<span class="meditor__output">
		<span class="meditor__output-label">Output preview</span>
		{#if !pathFormat}
			<span class="meditor__output-muted">Set a path format in Settings.</span>
		{:else if !previewTitle}
			<span class="meditor__output-muted">Add a title to preview.</span>
		{:else if previewPath}
			<code class="meditor__output-path">{previewPath}</code>
		{:else if previewError}
			<span class="meditor__output-muted">Preview unavailable.</span>
		{:else}
			<span class="meditor__output-muted">Previewing…</span>
		{/if}
	</span>
{/snippet}

{#snippet tagPolicy()}
	<details class="meditor__tagpolicy meditor__tagpolicy--toolbar">
		<summary>
			<span class="meditor__tagpolicy-title">Tag removal</span>
			<span class="meditor__tagpolicy-count" class:is-active={tagRemovalCount > 0}>
				{omittedLabel}
			</span>
		</summary>
		<p class="meditor__hint meditor__tagpolicy-hint">
			Omitted tags are not written to the output file. The catalog value still names the
			output unless you edit the field.
		</p>
		<div class="meditor__tagpolicy-grid">
			<div class="meditor__tagpolicy-group">
				<h4 class="meditor__tagpolicy-group-title">Identity</h4>
				{@render tagPolicyRow('title', 'Title', 'title tag')}
				{@render tagPolicyRow('subtitle', 'Subtitle', 'subtitle tag')}
				{@render tagPolicyRow('series', 'Series', 'series tag')}
				{@render tagPolicyRow('year', 'Year', 'year tag')}
			</div>
			<div class="meditor__tagpolicy-group">
				<h4 class="meditor__tagpolicy-group-title">People</h4>
				{@render tagPolicyRow('authors', 'Authors', 'author tags')}
				{@render tagPolicyRow('narrators', 'Narrators', 'narrator tags')}
			</div>
			<div class="meditor__tagpolicy-group">
				<h4 class="meditor__tagpolicy-group-title">Content</h4>
				{@render tagPolicyRow('description', 'Description', 'description tag')}
				{@render tagPolicyRow('genres', 'Genres', 'genre tags')}
			</div>
			<div class="meditor__tagpolicy-group">
				<h4 class="meditor__tagpolicy-group-title">Structure</h4>
				{#if isEditedChapters && !chaptersCleared}
					<span class="meditor__tagpolicy-note">Chapters — remove the custom list first</span>
				{:else}
					<label class="meditor__tagpolicy-row">
						<input
							type="checkbox"
							checked={chaptersCleared}
							aria-label="Remove chapter tags from output"
							onchange={(e) => {
								if (e.currentTarget.checked) {
									setClear('chapters', true);
								} else {
									// Unchecking only drops the clear marker,
									// NOT the list: ensureChapters may have
									// populated draft.chapters (from the
									// catalog) while the marker was set, and
									// an edited list must survive unchecking.
									// Exception: an EMPTY edited list has
									// nothing to restore TO. Return to the
									// INHERITED state (draft.chapters =
									// null) rather than re-assigning a
									// copied catalog list: null un-sticks
									// chaptersCleared, and a save sends
									// neither a pin (a copied list) nor a
									// clear marker (an empty list) — the
									// catalog chapters simply apply again.
									if (draft && draft.chapters !== null && draft.chapters.length === 0) {
										draft.chapters = null;
									}
									setClear('chapters', false);
								}
							}}
						/>
						<span>Chapters</span>
					</label>
				{/if}
			</div>
		</div>
	</details>
{/snippet}

<svelte:window onkeydown={handleGlobalKeydown} />

{#if loadError}
	<Alert variant="error">
		Could not load this book's metadata: {loadError.message}
		<ErrorDetail raw={loadError.raw} needsSettings={loadError.needsSettings} />
	</Alert>
{:else if !meta || !draft}
	<p class="meditor__loading" role="status">Loading metadata…</p>
{:else}
	<!-- The outer guard guarantees meta and draft are non-null here. The
	     snapshot may legitimately be null for a HISTORICAL book (no capture
	     row yet — the save that follows creates it); the empty fallback keeps
	     the catalog columns and cover/genre/chapter reads from crashing
	     before that save (finding: snapshot:null must render). -->
	{@const d = draft as BookMetadataDraft}
	{@const snap = (snapshot ?? ({
		book_id: 0,
		audiobookdb_book_id: '',
		audiobookdb_release_id: '',
		title: '',
		subtitle: '',
		authors: [],
		narrators: [],
		series_name: '',
		series_position: '',
		description: '',
		genres: [],
		year: null,
		cover_file: '',
		chapters: null,
		captured_at: ''
	}) as BookMetadataSnapshot)}

	<div class="meditor">
		<!-- ── Sticky commit row ───────────────────────────────────
		 * Visible before any scrolling and pinned under the nav while the
		 * form is in view. The status is text, not a colour-only dot. -->
		<div class="meditor__commit">
		<div class="meditor__toolbar">
			{#if confirmingDiscard}
				<div class="meditor__confirm" role="group" aria-label="Discard unsaved changes">
					<span>Discard unsaved changes and return to the saved values?</span>
					<div class="meditor__confirm-actions">
						<Button variant="danger" disabled={saving || resetting} onclick={discardChanges}>
							Yes, discard
						</Button>
						<Button variant="ghost" onclick={() => (confirmingDiscard = false)}>Cancel</Button>
					</div>
				</div>
			{:else if confirmingReset}
				<div class="meditor__confirm" role="group" aria-label="Reset to catalog">
					<span>Discard all manual edits and use the catalog values again? (Your chosen release is kept.)</span>
					<div class="meditor__confirm-actions">
						<Button
							variant="danger"
							loading={resetting}
							disabled={uploading || saving}
							onclick={doReset}
						>
							Yes, reset
						</Button>
						<Button variant="ghost" onclick={() => (confirmingReset = false)}>Cancel</Button>
					</div>
				</div>
			{:else}
				<div class="meditor__statuswrap">
					<span
						class="meditor__status"
						class:is-dirty={dirty}
						class:is-empty={!hasSavedOverrides && !dirty && !saving && !resetting}
						role="status"
						aria-live="polite"
					>
						{statusText}
					</span>
				</div>
				<div class="meditor__actions">
					<Button
						variant="primary"
						loading={saving}
						disabled={!dirty || chapterErrors.length > 0 || uploading}
						aria-label="Save manual metadata"
						title="Save manual metadata (Ctrl+S or Cmd+S)"
						aria-keyshortcuts="Control+S Meta+S"
						onclick={save}
					>
						<span class="meditor__label-long">Save manual metadata</span>
						<span class="meditor__label-short" aria-hidden="true">Save</span>
					</Button>
					{#if dirty}
						<Button
							variant="ghost"
							disabled={saving || resetting || uploading}
							aria-label="Discard unsaved changes"
							onclick={() => (confirmingDiscard = true)}
						>
							<span class="meditor__label-long">Discard changes</span>
							<span class="meditor__label-short" aria-hidden="true">Discard</span>
						</Button>
					{/if}
					<Button
						variant="ghost"
						class="meditor__reset-bar"
						disabled={!hasSavedOverrides || uploading || saving || resetting}
						aria-label="Reset to catalog"
						onclick={() => (confirmingReset = true)}
					>
						<span class="meditor__label-long">Reset to catalog</span>
						<span class="meditor__label-short" aria-hidden="true">Reset</span>
					</Button>
				</div>
			{/if}

			{#if saveError}
				<div class="meditor__toolbar-alert">
					<Alert variant="error">
						{saveError.message}
						<ErrorDetail raw={saveError.raw} needsSettings={saveError.needsSettings} />
					</Alert>
				</div>
			{/if}
		</div>
		{#if !confirmingDiscard && !confirmingReset}
			<div class="meditor__preview">
				{@render outputPreview()}
				{@render tagPolicy()}
			</div>
		{/if}
		</div>

		<!-- ── Catalog provenance and save contract ─────────────── -->
		{#if !snapshot}
			<Alert variant="info">
				This book has no captured catalog record yet, so the catalog columns are
				empty. Saving your edits below captures the catalog release and makes
				this book queueable.
			</Alert>
		{:else}
			<p class="meditor__provenance">
				<AbdTag />
				<span>Fields start with the catalog values. Edit anything, then save; the next processing run writes the tags.</span>
			</p>
		{/if}



		<!-- ── Book details ─────────────────────────────────────── -->
		<div class="meditor__groups">
		<section class="meditor__group" aria-labelledby="meditor-group-details">
			<h3 class="meditor__group-title" id="meditor-group-details">Book details</h3>

			<fieldset class="meditor__block">
				<legend class="sr-only">Title and subtitle</legend>
				<div class="meditor__row">
					<div class="meditor__fieldgroup">
						<label class="meditor__label" for="meditor-title">
							Title{#if isCleared('title')}<span class="meditor__omission">Tag omitted</span>{/if}
						</label>
						<input
							id="meditor-title"
							class="meditor__input"
							type="text"
							bind:value={d.title}
							placeholder="Title"
						/>
					</div>
					<div class="meditor__fieldgroup">
						<label class="meditor__label" for="meditor-subtitle">
							Subtitle{#if isCleared('subtitle')}<span class="meditor__omission">Tag omitted</span>{/if}
						</label>
						<input
							id="meditor-subtitle"
							class="meditor__input"
							type="text"
							bind:value={d.subtitle}
							placeholder="Subtitle"
						/>
					</div>
				</div>
			</fieldset>

			<fieldset class="meditor__block">
				<legend class="sr-only">Series and year</legend>
				<div class="meditor__row meditor__row--series">
					<div class="meditor__fieldgroup">
						<label class="meditor__label" for="meditor-series-name">
							Series name{#if isCleared('series')}<span class="meditor__omission">Tag omitted</span>{/if}
						</label>
						<input
							id="meditor-series-name"
							class="meditor__input"
							type="text"
							bind:value={d.series_name}
							placeholder="Series name"
						/>
					</div>
					<div class="meditor__fieldgroup">
						<label class="meditor__label" for="meditor-series-pos">Series position</label>
						<input
							id="meditor-series-pos"
							class="meditor__input"
							type="text"
							bind:value={d.series_position}
							placeholder="Series position"
						/>
					</div>
					<div class="meditor__fieldgroup">
						<label class="meditor__label" for="meditor-year">
							Year{#if isCleared('year')}<span class="meditor__omission">Tag omitted</span>{/if}
						</label>
						<input
							id="meditor-year"
							class="meditor__input meditor__input--year"
							type="number"
							min="1000"
							max="2100"
							step="1"
							value={d.year ?? ''}
							placeholder="Year"
							onchange={(e) => {
								const v = (e.currentTarget as HTMLInputElement).value;
								d.year = v === '' ? null : Number(v);
							}}
						/>
					</div>
				</div>
			</fieldset>

			<fieldset class="meditor__block">
				<legend class="sr-only">Description</legend>
				<div class="meditor__fieldgroup">
					<label class="meditor__label" for="meditor-description">
						Description{#if isCleared('description')}<span class="meditor__omission">Tag omitted</span>{/if}
					</label>
					<textarea
						id="meditor-description"
						class="meditor__input meditor__input--area"
						rows="4"
						bind:value={d.description}
						placeholder="Description"
					></textarea>
				</div>
			</fieldset>

			<fieldset class="meditor__block">
				<legend class="sr-only">Genres</legend>
				<div class="meditor__fieldgroup">
					<span class="meditor__label" id="meditor-genres-label">
						Genres{#if isCleared('genres')}<span class="meditor__omission">Tag omitted</span>{/if}
					</span>
					{#if (d.genres ?? []).length === 0}
						<p class="meditor__hint">No genres yet.</p>
					{:else}
						<ul class="meditor__chips" role="list" aria-labelledby="meditor-genres-label">
							{#each d.genres ?? [] as genre, i (i + ':' + genre)}
								<li class="meditor__chip">
									{genre}
									<button
										type="button"
										class="meditor__chip-remove"
										aria-label="Remove genre {genre}"
										onclick={() => removeGenre(i)}
									>
										<Trash2 class="h-3.5 w-3.5" aria-hidden="true" />
									</button>
								</li>
							{/each}
						</ul>
					{/if}
					<div class="meditor__list-actions meditor__list-actions--addrow">
						<input
							class="meditor__input meditor__input--add"
							type="text"
							bind:this={genreInput}
							placeholder="Add a genre"
							onkeydown={addGenre}
							aria-label="Add a genre"
						/>
						<Button
							variant="secondary"
							onclick={() => genreInput && commitGenreInput(genreInput)}
						>
							Add genre
						</Button>
					</div>
				</div>
			</fieldset>


		</section>

		<!-- ── People ───────────────────────────────────────────── -->
		<section class="meditor__group" aria-labelledby="meditor-group-people">
			<h3 class="meditor__group-title" id="meditor-group-people">People</h3>
			<p class="meditor__hint meditor__hint--group">
				The order here is the order in the output file and file name.
			</p>

			{#each peopleSections as { key, label } (key)}
				<fieldset class="meditor__block">
					<legend class="meditor__block-title">
						{label}{#if isCleared(key)}<span class="meditor__omission">Tag omitted</span>{/if}
					</legend>
					{#if (d[key] ?? []).length === 0}
						<p class="meditor__hint">No {personNoun(label)}s yet.</p>
					{:else}
						<ul class="meditor__people" role="list">
							{#each d[key] ?? [] as person, i (i + ':' + person)}
								<li class="meditor__people-row">
									<span class="meditor__people-order">{i + 1}</span>
									<input
										class="meditor__input meditor__input--person"
										type="text"
										value={person}
										aria-label="{personNoun(label)} {i + 1}"
										onchange={(e) => {
											const list = [...(d[key] ?? [])];
											list[i] = (e.currentTarget as HTMLInputElement).value;
											d[key] = list;
										}}
									/>
									<div class="meditor__people-actions">
										<button
											type="button"
											class="meditor__iconbtn"
											disabled={i === 0}
											aria-label="Move {personNoun(label)} {i + 1} up"
											onclick={() => movePerson(key, i, -1)}
										>
											<ChevronUp class="h-4 w-4" aria-hidden="true" />
										</button>
										<button
											type="button"
											class="meditor__iconbtn"
											disabled={i === (d[key] ?? []).length - 1}
											aria-label="Move {personNoun(label)} {i + 1} down"
											onclick={() => movePerson(key, i, 1)}
										>
											<ChevronDown class="h-4 w-4" aria-hidden="true" />
										</button>
										<button
											type="button"
											class="meditor__iconbtn meditor__iconbtn--danger"
											aria-label="Remove {personNoun(label)} {i + 1}"
											onclick={() => removePerson(key, i)}
										>
											<Trash2 class="h-4 w-4" aria-hidden="true" />
										</button>
									</div>
								</li>
							{/each}
						</ul>
					{/if}
					<div class="meditor__list-actions">
						<Button variant="ghost" onclick={() => addPerson(key)}>
							Add {personNoun(label)}
						</Button>
					</div>
				</fieldset>
			{/each}
		</section>

		<!-- ── Artwork & chapters ───────────────────────────────── -->
		<section class="meditor__group" aria-labelledby="meditor-group-media">
			<h3 class="meditor__group-title" id="meditor-group-media">Artwork</h3>

			<fieldset class="meditor__block">
				<legend class="meditor__block-title">Cover</legend>
				<div class="meditor__cover">
					<div class="meditor__cover-art">
						<CoverArt url={coverPreviewUrl} title={snap.title} size="sm" />
					</div>
					<div class="meditor__cover-main">
						<div class="meditor__cover-summary">
							<span class="meditor__cover-current">{coverSummary}</span>
							<Button
								variant="ghost"
								aria-expanded={coverOpen}
								onclick={() => (coverOpen = !coverOpen)}
							>
								{coverOpen ? 'Hide cover options' : 'Change cover'}
							</Button>
						</div>
						{#if coverOpen}
							<div class="meditor__cover-opts">
								<label class="meditor__radio">
									<input
										type="radio"
										name="meditor-cover"
										checked={coverSource === ''}
										onchange={() => setCoverSource('')}
									/>
									Catalog cover{snapshot?.cover_file ? '' : ' (none)'}
								</label>
								<label class="meditor__radio">
									<input
										type="radio"
										name="meditor-cover"
										checked={coverSource === 'file'}
										onchange={() => setCoverSource('file')}
									/>
									Upload artwork
								</label>
								<label class="meditor__radio">
									<input
										type="radio"
										name="meditor-cover"
										checked={coverSource === 'url'}
										onchange={() => setCoverSource('url')}
									/>
									Cover URL
								</label>
								<label class="meditor__radio">
									<input
										type="radio"
										name="meditor-cover"
										checked={coverSource === 'clear'}
										onchange={() => setCoverSource('clear')}
									/>
									Remove cover from output
								</label>
								{#if coverSource === 'file'}
									<div class="meditor__cover-file">
										<input
											bind:this={coverInput}
											type="file"
											accept="image/jpeg"
											class="meditor__cover-fileinput"
											aria-hidden="true"
											tabindex="-1"
											onchange={onCoverFile}
										/>
										<Button variant="secondary" loading={uploading} onclick={() => coverInput?.click()}>
											Choose a JPEG
										</Button>
										{#if d.cover_file}
											<code class="meditor__cover-name">{d.cover_file}</code>
										{/if}
									</div>
								{/if}
								{#if coverSource === 'url'}
									<input
										class="meditor__input"
										type="url"
										bind:value={d.cover_url}
										placeholder="https://…/cover.jpg"
										aria-label="Cover URL"
									/>
								{/if}
								{#if coverSource === 'clear'}
									<p class="meditor__hint">The output will have no cover art.</p>
								{/if}
							</div>
						{/if}
					</div>
				</div>
				{#if coverSource === 'file'}
					<p class="meditor__hint">
						Uploaded artwork is stored as a fixed file — re-uploading makes a new asset and never
						rewrites one a queued job already names.
					</p>
				{/if}
			</fieldset>


		</section>
		</div>

		<!-- ── Chapters: edit in place, five at a time ─────────── -->
		<section class="meditor__chapters-section" aria-labelledby="meditor-group-chapters">
			<div class="meditor__chapters-head">
				<div class="meditor__chapters-heading">
					<h3 class="meditor__group-title" id="meditor-group-chapters">
						Chapters{#if chaptersCleared}<span class="meditor__omission">Tag omitted from output</span>{/if}
					</h3>
					<p class="meditor__chapters-count">
						{#if chapterList.length <= 5}
							{chapterList.length} chapter{chapterList.length === 1 ? '' : 's'}
						{:else if chaptersExpanded}
							{chapterList.length} chapters
						{:else}
							Showing 5 of {chapterList.length} chapters
						{/if}
					</p>
				</div>
				<div class="meditor__chapters-tools">
					<div class="meditor__chapter-offset">
						<label for="meditor-chapter-offset">Offset all</label>
						<input
							id="meditor-chapter-offset"
							class="meditor__input meditor__input--millis"
							type="text"
							bind:value={chapterOffset}
							placeholder="-0:03:00"
						/>
						<Button variant="ghost" onclick={applyChapterOffset}>Apply</Button>
					</div>
					{#if draft && (draft.chapters !== null || chaptersCleared)}
						<Button variant="ghost" onclick={useCatalogChapters}>Use catalog chapters</Button>
					{/if}
				</div>
			</div>

			{#if chapterList.length === 0}
				<p class="meditor__hint meditor__hint--warn">
					No chapters — the chapter tags are removed from the output.
				</p>
			{:else}
				<div class="meditor__chapter-grid meditor__chapter-grid--head" aria-hidden="true">
					<span>#</span>
					<span>Title</span>
					<span>Start</span>
					<span>Duration</span>
					<span></span>
				</div>
				<ol class="meditor__chapters" role="list">
					{#each visibleChapters as c, i (i + ':' + c.title + c.start_millis)}
						<li
							class="meditor__chapter"
							class:meditor__chapter--dragging={draggingChapter === i}
							class:meditor__chapter--drop={dropChapter === i}
							ondragover={(e) => {
								e.preventDefault();
								if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
								dropChapter = i;
							}}
							ondragleave={() => {
								if (dropChapter === i) dropChapter = null;
							}}
							ondrop={(e) => {
								e.preventDefault();
								if (draggingChapter !== null) moveChapterTo(draggingChapter, i);
								draggingChapter = null;
								dropChapter = null;
							}}
						>
							<button
								type="button"
								class="meditor__chapter-order"
								draggable="true"
								data-chapter-grip={i}
								aria-label={`Reorder chapter ${i + 1}. Drag, or press Alt+ArrowUp or Alt+ArrowDown.`}
								title="Drag to reorder. Alt+Arrow moves one position."
								ondragstart={(e) => {
									draggingChapter = i;
									if (e.dataTransfer) {
										e.dataTransfer.effectAllowed = 'move';
										e.dataTransfer.setData('text/plain', String(i));
									}
								}}
								ondragend={() => {
									draggingChapter = null;
									dropChapter = null;
								}}
								onkeydown={(e) => void handleChapterKeydown(e, i)}
							>
								{i + 1}
							</button>
							<input
								class="meditor__input meditor__chapter-title"
								type="text"
								value={c.title}
								placeholder="Chapter title"
								aria-label="Chapter {i + 1} title"
								onchange={(e) => {
									if (!draft) return;
									const list = ensureChapters();
									list[i] = { ...list[i], title: (e.currentTarget as HTMLInputElement).value };
									draft.chapters = [...list];
								}}
							/>
							<input
								class="meditor__input meditor__input--millis meditor__chapter-start"
								type="text"
								value={formatChapterTime(c.start_millis)}
								placeholder="0:04:12"
								aria-label="Chapter {i + 1} start (h:mm:ss or milliseconds)"
								onchange={(e) => {
									if (!draft) return;
									const parsed = parseChapterTime((e.currentTarget as HTMLInputElement).value);
									if (parsed === null) {
										chapterTimeError = 'Use h:mm:ss or milliseconds for chapter times.';
										(e.currentTarget as HTMLInputElement).value = formatChapterTime(c.start_millis);
										return;
									}
									chapterTimeError = '';
									const list = ensureChapters();
									list[i] = { ...list[i], start_millis: parsed };
									draft.chapters = [...list];
								}}
							/>
							<input
								class="meditor__input meditor__input--millis meditor__chapter-duration"
								type="text"
								value={formatChapterTime(c.duration_millis)}
								placeholder="0:04:12"
								aria-label="Chapter {i + 1} duration (h:mm:ss or milliseconds)"
								onchange={(e) => {
									if (!draft) return;
									const parsed = parseChapterTime((e.currentTarget as HTMLInputElement).value);
									if (parsed === null) {
										chapterTimeError = 'Use h:mm:ss or milliseconds for chapter times.';
										(e.currentTarget as HTMLInputElement).value = formatChapterTime(c.duration_millis);
										return;
									}
									chapterTimeError = '';
									const list = ensureChapters();
									list[i] = { ...list[i], duration_millis: parsed };
									draft.chapters = [...list];
								}}
							/>
							<div class="meditor__chapter-actions">
								<button
									type="button"
									class="meditor__iconbtn"
									disabled={i === 0}
									aria-label="Move chapter {i + 1} up"
									onclick={() => moveChapter(i, -1)}
								>
									<ChevronUp class="h-4 w-4" aria-hidden="true" />
								</button>
								<button
									type="button"
									class="meditor__iconbtn"
									disabled={i === chapterList.length - 1}
									aria-label="Move chapter {i + 1} down"
									onclick={() => moveChapter(i, 1)}
								>
									<ChevronDown class="h-4 w-4" aria-hidden="true" />
								</button>
								<button
									type="button"
									class="meditor__iconbtn meditor__iconbtn--danger"
									aria-label="Remove chapter {i + 1}"
									onclick={() => removeChapter(i)}
								>
									<Trash2 class="h-4 w-4" aria-hidden="true" />
								</button>
							</div>
						</li>
					{/each}
				</ol>
				{#if chapterList.length > 5}
					<Button
						variant="ghost"
						aria-expanded={chaptersExpanded}
						onclick={() => (chaptersExpanded = !chaptersExpanded)}
					>
						{chaptersExpanded
							? 'Show first 5 chapters'
							: `Show ${chapterList.length - 5} more chapter${chapterList.length - 5 === 1 ? '' : 's'}`}
					</Button>
				{/if}
			{/if}
			{#if chapterTimeError}
				<p class="meditor__chapter-time-error" role="alert">{chapterTimeError}</p>
			{/if}
			{#if chapterErrors.length}
				<ul class="meditor__chapter-errors" role="alert">
					{#each chapterErrors as problem}
						<li>{problem}</li>
					{/each}
				</ul>
			{/if}
			<div class="meditor__list-actions">
				<Button variant="ghost" onclick={addChapter}>Add chapter</Button>
				<button type="button" class="meditor__textdanger" onclick={removeAllChapters}>
					Remove chapter tags
				</button>
			</div>
		</section>

		<div class="meditor__footer">
			<Button
				variant="ghost"
				class="meditor__reset-footer"
				disabled={!hasSavedOverrides || uploading || saving || resetting}
				aria-label="Reset to catalog"
				onclick={() => (confirmingReset = true)}
			>
				<span class="meditor__label-long">Reset to catalog</span>
				<span class="meditor__label-short" aria-hidden="true">Reset</span>
			</Button>
		</div>
	</div>
{/if}

<style>
	.meditor__loading {
		padding: 1rem 0;
		color: var(--text-muted);
	}

	/* ── Sticky commit block ───────────────────────────────────── */
	.meditor__commit {
		position: sticky;
		top: calc(var(--nav-height) + 0.5rem);
		z-index: 5;
		background: var(--bg-panel);
	}

	.meditor__toolbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem 1rem;
		flex-wrap: wrap;
		padding: 0.75rem;
		margin-bottom: 0;
		background: var(--bg-panel);
		border: 0;
		border-bottom: 1px solid var(--border);
		border-radius: 0;
	}

	.meditor__preview {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		padding: 0 0.75rem 0.75rem;
		margin-bottom: 0.875rem;
		background: var(--bg-panel);
		border-bottom: 1px solid var(--border);
	}

	.meditor__statuswrap {
		display: flex;
		flex: 1 1 26rem;
		flex-direction: column;
		gap: 0.25rem;
		min-width: 0;
	}

	.meditor__status {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 14px;
		font-weight: 600;
		color: var(--text-secondary);
	}

	.meditor__status::before {
		content: '';
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 9999px;
		background: var(--state-green);
		flex-shrink: 0;
	}

	.meditor__status.is-dirty {
		color: var(--state-amber);
	}

	.meditor__status.is-dirty::before {
		background: var(--state-amber);
	}

	.meditor__status.is-empty {
		color: var(--text-muted);
	}

	.meditor__status.is-empty::before {
		background: var(--text-muted);
	}

	.meditor__output {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
		min-width: 0;
		flex-wrap: wrap;
		font-size: 12px;
		color: var(--text-muted);
	}

	.meditor__output-label {
		font-size: 11px;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
		flex-shrink: 0;
	}

	.meditor__output-path {
		font-size: 12px;
		color: var(--text-secondary);
		overflow-wrap: anywhere;
		min-width: 0;
	}

	.meditor__output-muted {
		font-size: 12px;
		color: var(--text-muted);
	}

	.meditor__footer {
		display: none;
	}

	.meditor__actions {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
		flex: 0 0 auto;
		align-self: flex-start;
	}

	:global(.meditor__label-short) {
		display: none;
	}

	.meditor__confirm {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.625rem;
		flex-wrap: wrap;
		width: 100%;
		font-size: 14px;
		color: var(--text);
	}

	.meditor__confirm-actions {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.meditor__toolbar-alert {
		width: 100%;
	}

	/* Focus should not land under the sticky commit row. */
	.meditor input,
	.meditor textarea,
	.meditor summary {
		scroll-margin-top: calc(var(--nav-height) + 6rem);
	}

	.meditor__provenance {
		display: block;
		margin: 0.875rem 0 1.125rem;
		font-size: 13px;
		line-height: 1.5;
		color: var(--text-muted);
	}

	.meditor__provenance :global(.abd-tag) {
		margin-right: 0.35rem;
		vertical-align: -1px;
	}

	/* ── Groups ── */
	.meditor__group {
		margin: 0;
	}

	.meditor__group + .meditor__group {
		margin-top: 1.5rem;
		padding-top: 1.25rem;
		border-top: 1px solid var(--border-subtle);
	}

	@media (min-width: 1180px) {
		.meditor__groups {
			display: grid;
			grid-template-columns: minmax(0, 1.55fr) minmax(22rem, 1fr);
			gap: 1.5rem;
			align-items: start;
		}

		.meditor__groups > .meditor__group + .meditor__group {
			margin-top: 0;
			padding-top: 0;
			border-top: 0;
		}

		.meditor__groups > .meditor__group:nth-child(1) {
			grid-column: 1;
			grid-row: 1 / span 2;
		}

		.meditor__groups > .meditor__group:nth-child(2) {
			grid-column: 2;
			grid-row: 1;
		}

		.meditor__groups > .meditor__group:nth-child(3) {
			grid-column: 2;
			grid-row: 2;
			border-top: 1px solid var(--border-subtle);
			padding-top: 1.25rem;
		}
	}

	.meditor__group-title {
		margin: 0 0 0.75rem;
		font-size: 0.96875rem;
		font-weight: 700;
		line-height: 1.3;
		letter-spacing: -0.01em;
		color: var(--text);
	}

	/* ── Field groups: no nested boxes ── */
	.meditor__block {
		min-width: 0;
		margin: 0 0 1rem;
		padding: 0;
		border: 0;
	}

	.meditor__block:last-child {
		margin-bottom: 0;
	}

	.meditor__block-title {
		display: block;
		margin: 0 0 0.5rem;
		padding: 0;
		font-size: 14px;
		font-weight: 600;
		line-height: 1.35;
		color: var(--text);
	}

	.meditor__omission {
		font-size: 12px;
		font-weight: 500;
		line-height: 1.35;
		color: var(--text-secondary);
	}

	.meditor__block-title .meditor__omission {
		margin-left: 0.5rem;
	}

	/* ── Field grid: label above control ── */
	.meditor__row {
		display: grid;
		align-items: start;
		gap: 0.75rem 1rem;
	}

	.meditor__row--series {
		grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) minmax(0, 1fr);
	}

	.meditor__fieldgroup {
		display: grid;
		gap: 0.375rem;
		min-width: 0;
	}

	.meditor__label {
		font-size: 13.5px;
		font-weight: 600;
		line-height: 1.35;
		color: var(--text);
	}

	.meditor__label .meditor__omission {
		margin-left: 0.4rem;
		font-weight: 500;
	}

	.meditor__input {
		width: 100%;
		min-height: 44px;
		padding: 0.5rem 0.75rem;
		font-size: 14px;
		font-family: inherit;
		color: var(--text);
		background: var(--surface);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
	}

	.meditor__input:hover {
		border-color: var(--text-muted);
	}

	.meditor__input:focus {
		outline: 3px solid var(--focus-ring);
		outline-offset: 1px;
		border-color: var(--text);
	}

	.meditor__input--year,
	.meditor__input--millis {
		width: 6.5rem;
		min-width: 6.5rem;
		text-align: right;
		font-variant-numeric: tabular-nums;
	}

	.meditor__input--year {
		flex: 1 1 auto;
		width: 100%;
		min-width: 0;
		text-align: left;
	}

	.meditor__input--area {
		min-height: 6.5rem;
		max-width: 65ch;
		resize: vertical;
		line-height: 1.5;
	}

	.meditor__input--person {
		flex: 1 1 12rem;
		min-width: 0;
	}

	.meditor__input--add {
		flex: 1 1 12rem;
		min-width: 0;
	}

	/* ── Tag policy: one grouped disclosure, not a per-row wall ── */
	.meditor__tagpolicy {
		margin: 0 0 1.25rem;
		padding: 0;
		border-top: 1px solid var(--border-subtle);
		border-bottom: 1px solid var(--border-subtle);
	}

	.meditor__tagpolicy--toolbar {
		margin: 0.25rem 0 0;
		padding-top: 0.25rem;
		border-top: 0;
		border-bottom: 0;
	}

	.meditor__tagpolicy--toolbar summary {
		min-height: 2rem;
		padding: 0.25rem 0;
		font-size: 12px;
	}

	.meditor__tagpolicy--toolbar .meditor__tagpolicy-grid {
		max-height: 14rem;
		overflow: auto;
		padding-top: 0.25rem;
	}

	@media (min-width: 900px) {
		.meditor__tagpolicy--toolbar .meditor__tagpolicy-grid {
			grid-template-columns: repeat(4, minmax(0, 1fr));
		}
	}

	.meditor__tagpolicy summary {
		min-height: 2.75rem;
		padding: 0.5rem 0;
		font-size: 13px;
		font-weight: 600;
		color: var(--text);
		cursor: pointer;
	}

	.meditor__tagpolicy summary::marker {
		color: var(--workspace-action);
	}

	.meditor__tagpolicy-count {
		float: right;
		font-size: 12px;
		font-weight: 500;
		color: var(--text-muted);
	}

	.meditor__tagpolicy-count.is-active {
		color: var(--state-amber);
		font-weight: 600;
	}

	.meditor__tagpolicy-hint {
		margin: 0 0 0.75rem;
	}

	.meditor__tagpolicy-grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 1.25rem 2rem;
		padding: 0.25rem 0 0.75rem;
	}

	.meditor__tagpolicy-group {
		min-width: 0;
	}

	.meditor__tagpolicy-group-title {
		margin: 0 0 0.25rem;
		font-size: 11px;
		font-weight: 600;
		letter-spacing: 0.05em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.meditor__tagpolicy-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.75rem;
		font-size: 13px;
		color: var(--text-secondary);
		cursor: pointer;
	}

	.meditor__tagpolicy-row input {
		margin: 0;
		flex-shrink: 0;
	}

	.meditor__tagpolicy-row:has(input:checked) {
		color: var(--text);
		font-weight: 600;
	}

	.meditor__tagpolicy-row:has(input:focus-visible) {
		outline: 3px solid var(--focus-ring);
		outline-offset: 2px;
		border-radius: var(--radius-sm);
	}

	.meditor__tagpolicy-note {
		display: flex;
		align-items: center;
		min-height: 2.75rem;
		font-size: 12px;
		color: var(--text-muted);
	}

	/* ── Hints and disclosures ── */
	.meditor__hint {
		margin: 0.5rem 0 0;
		font-size: 13px;
		line-height: 1.5;
		color: var(--text-muted);
	}

	.meditor__hint--group {
		margin: -0.25rem 0 0.75rem;
	}

	.meditor__hint--warn {
		color: var(--state-amber);
	}

	/* ── People, genres ── */
	.meditor__list-actions {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
		margin-top: 0.5rem;
	}

	.meditor__list-actions--addrow {
		margin-top: 0;
	}

	.meditor__people {
		list-style: none;
		margin: 0 0 0.625rem;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.meditor__people-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	.meditor__people-order {
		width: 1.25rem;
		text-align: center;
		font-size: 12px;
		font-weight: 600;
		color: var(--text-muted);
		font-variant-numeric: tabular-nums;
		flex-shrink: 0;
	}

	.meditor__people-actions {
		display: flex;
		gap: 0.125rem;
		flex-shrink: 0;
		margin-left: auto;
	}

	.meditor__iconbtn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 2.75rem;
		height: 2.75rem;
		color: var(--text-secondary);
		background: transparent;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		cursor: pointer;
	}

	.meditor__iconbtn:hover:not(:disabled) {
		background: var(--surface-hover);
		border-color: var(--border);
	}

	.meditor__iconbtn:focus-visible {
		outline: 3px solid var(--focus-ring);
		outline-offset: 1px;
	}

	.meditor__iconbtn:disabled {
		opacity: 0.3;
		cursor: not-allowed;
	}

	.meditor__iconbtn--danger:hover:not(:disabled) {
		color: var(--state-red);
		background: var(--state-red-bg);
		border-color: var(--state-red-border);
	}

	.meditor__chips {
		list-style: none;
		margin: 0 0 0.625rem;
		padding: 0;
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem;
	}

	.meditor__chip {
		display: inline-flex;
		align-items: center;
		gap: 0.25rem;
		padding: 0.25rem 0.375rem 0.25rem 0.625rem;
		font-size: 13.5px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}

	.meditor__chip-remove {
		display: inline-flex;
		padding: 0.375rem;
		color: var(--text-muted);
		background: transparent;
		border: none;
		border-radius: 4px;
		cursor: pointer;
	}

	.meditor__chip-remove:hover {
		color: var(--state-red);
		background: var(--state-red-bg);
	}

	.meditor__chip-remove:focus-visible {
		outline: 3px solid var(--focus-ring);
		outline-offset: 1px;
	}

	/* ── Cover ── */
	.meditor__cover {
		display: grid;
		grid-template-columns: 3rem minmax(0, 1fr);
		gap: 1rem;
		align-items: start;
	}

	.meditor__cover-main {
		min-width: 0;
	}

	.meditor__cover-summary {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		flex-wrap: wrap;
		min-height: 2.75rem;
	}

	.meditor__cover-current {
		font-size: 14px;
		font-weight: 600;
		color: var(--text);
	}

	.meditor__cover-opts {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		margin-top: 0.25rem;
	}

	.meditor__radio {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.75rem;
		font-size: 14px;
		cursor: pointer;
	}

	.meditor__radio input {
		margin: 0;
	}

	.meditor__cover-file {
		display: flex;
		align-items: center;
		gap: 0.625rem;
		flex-wrap: wrap;
		margin-top: 0.25rem;
	}

	.meditor__cover-fileinput {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
		white-space: nowrap;
	}

	.meditor__cover-name {
		font-size: 12px;
		color: var(--text-muted);
		overflow-wrap: anywhere;
	}

	/* ── Chapters ── */
	.meditor__chapters-section {
		margin-top: 1.5rem;
		padding-top: 1.5rem;
		border-top: 1px solid var(--border);
	}

	.meditor__chapters-head {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		flex-wrap: wrap;
		gap: 0.75rem 1rem;
		margin-bottom: 0.75rem;
	}

	.meditor__chapters-heading {
		display: flex;
		align-items: baseline;
		flex-wrap: wrap;
		gap: 0.25rem 0.75rem;
	}

	.meditor__chapters-heading .meditor__group-title {
		margin: 0;
	}

	.meditor__chapters-count {
		margin: 0;
		font-size: 13px;
		color: var(--text-muted);
		font-variant-numeric: tabular-nums;
	}

	.meditor__chapters-tools {
		display: flex;
		align-items: center;
		flex-wrap: wrap;
		gap: 0.5rem 1rem;
	}

	.meditor__chapter-offset {
		display: flex;
		align-items: center;
		gap: 0.375rem;
		font-size: 13px;
		color: var(--text-muted);
	}

	.meditor__chapter-offset .meditor__input {
		width: 6.5rem;
	}

	.meditor__chapter-grid,
	.meditor__chapter {
		display: grid;
		grid-template-columns: 2rem minmax(0, 1fr) 7rem 7rem auto;
		align-items: center;
		gap: 0.5rem;
	}

	.meditor__chapter-grid--head {
		padding: 0 0.5rem 0.375rem;
		font-size: 11px;
		font-weight: 700;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--text-muted);
	}

	.meditor__chapters {
		list-style: none;
		margin: 0 0 0.625rem;
		padding: 0;
		border-top: 1px solid var(--border-subtle);
	}

	.meditor__chapter {
		padding: 0.375rem 0.5rem;
		border-bottom: 1px solid var(--border-subtle);
	}

	.meditor__chapter-order {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 2rem;
		height: 2.75rem;
		padding: 0;
		font-family: inherit;
		font-size: 12px;
		font-weight: 600;
		color: var(--text-muted);
		font-variant-numeric: tabular-nums;
		background: transparent;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		cursor: grab;
	}

	.meditor__chapter-order:hover {
		background: var(--surface-hover);
	}

	.meditor__chapter-order:active {
		cursor: grabbing;
	}

	.meditor__chapter-order:focus-visible {
		outline: 3px solid var(--focus-ring);
		outline-offset: 1px;
	}

	.meditor__chapter--dragging {
		opacity: 0.5;
	}

	.meditor__chapter--drop {
		background: var(--surface-hover);
		outline: 2px solid var(--focus-ring);
		outline-offset: -2px;
		border-radius: var(--radius-sm);
	}

	.meditor__chapter-title {
		min-width: 0;
	}

	.meditor__chapter-start,
	.meditor__chapter-duration {
		width: 100%;
		min-width: 0;
		font-variant-numeric: tabular-nums;
	}

	.meditor__chapter-actions {
		display: flex;
		justify-content: flex-end;
		gap: 0.125rem;
	}

	.meditor__chapter-time-error {
		margin: 0 0 0.625rem;
		font-size: 13px;
		color: var(--state-red);
	}

	.meditor__chapter-errors {
		margin: 0 0 0.625rem;
		padding-left: 1.25rem;
		font-size: 13px;
		color: var(--state-red);
		display: flex;
		flex-direction: column;
		gap: 0.125rem;
	}

	.meditor__textdanger {
		display: inline-flex;
		align-items: center;
		min-height: 2.75rem;
		padding: 0.5rem 0.75rem;
		font-size: 13px;
		font-weight: 600;
		color: var(--state-red);
		background: transparent;
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
		cursor: pointer;
	}

	.meditor__textdanger:hover {
		background: var(--state-red-bg);
		border-color: var(--state-red-border);
	}

	.meditor__textdanger:focus-visible {
		outline: 3px solid var(--focus-ring);
		outline-offset: 1px;
	}

	@media (max-width: 720px) {
		.meditor__commit {
			/* display:contents lifts the toolbar's containing block from
			     this short wrapper (toolbar+preview only) to .meditor,
			     so the sticky toolbar pins through the whole form
			     instead of scrolling away after the preview. */
			display: contents;
		}

		.meditor__toolbar {
			position: sticky;
			top: calc(var(--nav-height) + 0.25rem);
			z-index: 5;
			display: grid;
			grid-template-columns: minmax(0, 1fr) auto;
			align-items: center;
			gap: 0.25rem 0.5rem;
			padding: 0.5rem 0.625rem;
		}

		.meditor__preview {
			padding: 0.5rem 0.625rem 0.75rem;
		}

		.meditor__statuswrap {
			display: contents;
		}

		.meditor__status {
			grid-column: 1;
			grid-row: 1;
			font-size: 13px;
			line-height: 1.3;
		}

		.meditor__actions {
			grid-column: 2;
			grid-row: 1;
			width: auto;
			flex-wrap: nowrap;
		}

		.meditor__confirm,
		.meditor__toolbar-alert {
			grid-column: 1 / -1;
		}

		:global(.meditor__reset-bar) {
			display: none;
		}

		.meditor__footer {
			display: flex;
			justify-content: flex-end;
			margin-top: 1rem;
			padding-top: 0.75rem;
			border-top: 1px solid var(--border-subtle);
		}

		:global(.meditor__label-long) {
			display: none;
		}

		:global(.meditor__label-short) {
			display: inline;
		}

		.meditor__row {
			grid-template-columns: minmax(0, 1fr);
			gap: 0.375rem;
			margin-bottom: 0.875rem;
		}

		.meditor__row--series {
			grid-template-columns: minmax(0, 1fr);
			gap: 0.75rem;
		}

		.meditor__tagpolicy-grid {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}

		.meditor input,
		.meditor textarea,
		.meditor summary {
			scroll-margin-top: calc(var(--nav-height) + 11rem);
		}

	}

	@media (max-width: 560px) {
		.meditor__chapter-grid--head {
			display: none;
		}

		.meditor__chapter-grid,
		.meditor__chapter {
			grid-template-columns: 2rem minmax(0, 1fr) 8.5rem;
			grid-template-areas:
				'order title title'
				'. start duration'
				'. actions actions';
			row-gap: 0.375rem;
		}

		.meditor__chapter-order {
			grid-area: order;
		}

		.meditor__chapter-title {
			grid-area: title;
		}

		.meditor__chapter-start {
			grid-area: start;
		}

		.meditor__chapter-duration {
			grid-area: duration;
		}

		.meditor__chapter-actions {
			grid-area: actions;
		}

		.meditor__tagpolicy-grid {
			grid-template-columns: 1fr;
		}
	}


	@media (prefers-reduced-motion: reduce) {
		.meditor__input {
			transition: none;
		}
	}
</style>
