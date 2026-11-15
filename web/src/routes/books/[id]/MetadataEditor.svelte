<script lang="ts">
	import { get, put, post } from '#lib/api.js';
	import {
		bookCoverUrl,
		type BookMetadataResponse,
		type BookMetadataDraft,
		type BookMetadataSnapshot,
		type CoverUploadResponse,
		type EnginePreviewResponse,
		type Settings,
		type SnapshotChapter,
	} from '#lib/types.js';
	import { cleanAdbError, toCleanedError, type CleanedAdbError } from '#lib/adbError.js';
	import { Button, Alert, CoverArt, ErrorDetail } from '#lib/components/index.js';
	import { Plus, Trash2, ChevronUp, ChevronDown } from '@lucide/svelte';

	interface Props {
		bookId: number;
		/** Global path format for the naming preview; null while loading. */
		pathFormat: string | null;
		/** Set when a save completed, so the caller can note the state. */
		onSaved?: () => void;
	}

	let { bookId, pathFormat, onSaved }: Props = $props();

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
	let savedNote = $state(false);
	let confirmingReset = $state(false);
	let resetting = $state(false);

	let coverInput = $state<HTMLInputElement | null>(null);
	let uploading = $state(false);

	// Debounced engine naming preview of the DRAFT (what the next job
	// would name the output from) — the engine owns the resolver.
	let previewPath = $state('');
	let previewSeq = 0;

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
		meta = null;
		draft = null;
		// A stale upload from the previous book cannot clear its own
		// `finally` (the generation guard skips it); a new generation owns
		// a fresh flag so the new book never inherits a stuck loading state.
		uploading = false;
		get<BookMetadataResponse>(`/books/${id}/metadata`)
			.then((resp) => {
				if (gen !== bookFetchSeq) return;
				meta = resp;
				draft = draftFrom(resp);
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
		return {
			title: ov?.title ?? '',
			subtitle: ov?.subtitle ?? '',
			authors: ov?.authors ?? null,
			narrators: ov?.narrators ?? null,
			series_name: ov?.series_name ?? '',
			series_position: ov?.series_position ?? '',
			description: ov?.description ?? '',
			genres: ov?.genres ?? null,
			year: ov?.year ?? null,
			cleared: ov?.cleared ?? [],
			cover_source: ov?.cover_source ?? '',
			cover_file: ov?.cover_file ?? '',
			cover_url: ov?.cover_url ?? '',
			chapters: ov?.chapters ?? null
		};
	}

	const snapshot = $derived(meta?.snapshot ?? null);

	// The two ordered-people sections, typed so the edit helpers (which
	// take the literal union, not string) stay sound in the template.
	const peopleSections = $derived([
		{ key: 'authors', label: 'Authors', catalog: snapshot?.authors ?? [] },
		{ key: 'narrators', label: 'Narrators', catalog: snapshot?.narrators ?? [] }
	] as const);

	// ── Edited / cleared indicators (server state, not a local guess) ──
	const isEditedScalar = (v: string) => v !== '';
	const isEditedList = (l: string[] | null) => l !== null;
	const isEditedChapters = $derived(draft?.chapters !== null);

	function isCleared(field: string): boolean {
		return draft?.cleared.includes(field) ?? false;
	}

	function toggleClear(field: string, allow: boolean) {
		if (!draft || !allow) return;
		draft.cleared = isCleared(field)
			? draft.cleared.filter((f) => f !== field)
			: [...draft.cleared, field];
	}

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

	$effect(() => {
		const fmt = pathFormat;
		const payload = previewMetadata;
		if (!draft || !fmt) return;
		const seq = ++previewSeq;
		previewPath = '';
		const timer = setTimeout(async () => {
			try {
				const resp = await post<EnginePreviewResponse>('/engine/preview', {
					metadata: payload,
					path_format: fmt
				});
				if (seq === previewSeq) previewPath = resp.relative_path;
			} catch {
				if (seq === previewSeq) previewPath = '';
			}
		}, 250);
		return () => clearTimeout(timer);
	});

	// ── Cover handling ───────────────────────────────────────────
	const coverPreviewUrl = $derived.by(() => {
		if (!draft) return null;
		if (draft.cover_source === 'file' && draft.cover_file) return `/covers/${draft.cover_file}`;
		if (draft.cover_source === 'url' && draft.cover_url) return draft.cover_url;
		if (draft.cover_source === 'clear') return null;
		return snapshot?.cover_file ? `/covers/${snapshot.cover_file}` : null;
	});

	async function onCoverFile(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const file = input.files?.[0];
		input.value = '';
		if (!file || !draft) return;
		uploading = true;
		saveError = null;
		// Capture the book + generation the upload started for: if the user
		// navigates (bookId changes, bookFetchSeq bumps) while the request is
		// in flight, the response must not be applied to the NEW book's draft
		// (it would set a cover file owned by the old book, which the server
		// rejects on save).
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
		const target = e.currentTarget as HTMLInputElement;
		if (e.key !== 'Enter' && e.key !== ',') return;
		e.preventDefault();
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
		const [item] = list.splice(index, 1);
		list.splice(target, 0, item);
		draft.chapters = [...list];
	}

	function useCatalogChapters() {
		if (!draft) return;
		draft.chapters = null;
		// A saved clear ("No chapters (clear tag)") must not survive the
		// operator choosing the catalog list again: the server only drops
		// the marker for a non-empty edited list, so the client strips it
		// here explicitly (finding: clear-tag stuck after returning to catalog).
		draft.cleared = draft.cleared.filter((f) => f !== 'chapters');
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
				problems.push(`${label}: times are whole milliseconds, not negative.`);
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

	/** "1h 02m 03s" from whole milliseconds, for the human-readable
	 *  time readout beside each integer-millisecond field. */
	function millisHuman(millis: number): string {
		const total = Math.max(0, Math.floor(millis / 1000));
		const h = Math.floor(total / 3600);
		const m = Math.floor((total % 3600) / 60);
		const s = total % 60;
		if (h) return `${h}h ${String(m).padStart(2, '0')}m ${String(s).padStart(2, '0')}s`;
		if (m) return `${m}m ${String(s).padStart(2, '0')}s`;
		return `${s}s`;
	}

	// ── Save / reset ─────────────────────────────────────────────
	async function save() {
		if (!draft || saving || uploading) return; // the server sweep races an in-flight upload
		if (chapterErrors.length) return; // diagnostics already visible
		saving = true;
		saveError = null;
		savedNote = false;
		try {
			const resp = await put<BookMetadataResponse>(`/books/${bookId}/metadata`, draft);
			meta = resp;
			onSaved?.();
			savedNote = true;
		} catch (e) {
			saveError = toCleanedError(e);
		} finally {
			saving = false;
		}
	}

	async function doReset() {
		if (resetting || uploading) return; // the server sweep races an in-flight upload
		resetting = true;
		saveError = null;
		try {
			await post(`/books/${bookId}/metadata/reset`, {});
			confirmingReset = false;
			reload();
			onSaved?.();
		} catch (e) {
			saveError = toCleanedError(e);
			confirmingReset = false;
		} finally {
			resetting = false;
		}
	}
</script>

{#if loadError}
	<Alert variant="error">
		Could not load this book's metadata: {loadError.message}
		<ErrorDetail raw={loadError.raw} needsSettings={loadError.needsSettings} />
	</Alert>
{:else if !meta || !draft}
	<p class="meditor__loading">Loading…</p>
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
	<!-- ── Naming preview (engine-backed, debounced) ────────────── -->
	{#if pathFormat}
		<div class="meditor__preview" role="status" aria-live="polite">
			<span class="meditor__preview-label">Output name</span>
			{#if previewPath}
				<code class="meditor__preview-path">{previewPath}</code>
			{:else}
				<span class="meditor__preview-muted">previewing…</span>
			{/if}
		</div>
	{:else}
		<div class="meditor__preview" role="status">
			<span class="meditor__preview-label">Output name</span>
			<span class="meditor__preview-muted">Set a path format in Settings to preview the output name.</span>
		</div>
	{/if}

	{#if !snapshot}
		<Alert variant="info">
			This book has no captured catalog record yet, so the catalog columns are
			empty. Saving your edits below captures the catalog release and makes
			this book queueable.
		</Alert>
	{/if}

	<!-- ── Title & subtitle ────────────────────────────────────── -->
	<fieldset class="meditor__block">
		<legend class="meditor__block-title">Title</legend>
		<div class="meditor__row">
			<label class="meditor__field" for="meditor-title">
				<span class="meditor__field-label">Title</span>
				<span class="meditor__field-catalog">{snap.title || '—'}</span>
			</label>
			<input
				id="meditor-title"
				class="meditor__input"
				type="text"
				bind:value={d.title}
				placeholder="Keep catalog title"
				aria-describedby="meditor-title-help"
			/>
			<button
				type="button"
				class="meditor__clearbtn"
				class:is-on={isCleared('title')}
				onclick={() => toggleClear('title', true)}
			>
				Clear tag
			</button>
		</div>
		<p class="meditor__hint" id="meditor-title-help">
			{#if isEditedScalar(d.title)}
				Writes this title to the output.{#if isCleared('title')} The tag is also cleared from the file.{/if}
			{:else if isCleared('title')}
				The title tag is cleared from the file; the catalog title keeps naming the output.
			{:else}
				Blank keeps the catalog title.
			{/if}
		</p>
		<div class="meditor__row">
			<label class="meditor__field" for="meditor-subtitle">
				<span class="meditor__field-label">Subtitle</span>
				<span class="meditor__field-catalog">{snap.subtitle || '—'}</span>
			</label>
			<input
				id="meditor-subtitle"
				class="meditor__input"
				type="text"
				bind:value={d.subtitle}
				placeholder="Keep catalog subtitle"
			/>
			<button
				type="button"
				class="meditor__clearbtn"
				class:is-on={isCleared('subtitle')}
				onclick={() => toggleClear('subtitle', true)}
			>
				Clear tag
			</button>
		</div>
	</fieldset>

	<!-- ── People (ordered) ────────────────────────────────────── -->
	{#each peopleSections as { key, label, catalog } (key)}
		{@const clearField = key}
		<fieldset class="meditor__block">
			<legend class="meditor__block-title">{label}</legend>
			{#if d[key] === null}
				<p class="meditor__list-catalog">Catalog: {catalog.join(', ') || '—'}</p>
				<Button variant="secondary" onclick={() => ensureList(key)}>Edit list</Button>
			{:else}
				<ul class="meditor__people" role="list">
					{#each d[key] as person, i (i + ':' + person)}
						<li class="meditor__people-row">
							<span class="meditor__people-order">{i + 1}</span>
							<input
								class="meditor__input meditor__input--person"
								type="text"
								value={person}
								aria-label="{label} {i + 1}"
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
									aria-label="Move {label.toLowerCase()} {i + 1} up"
									onclick={() => movePerson(key, i, -1)}
								>
									<ChevronUp class="h-4 w-4" aria-hidden="true" />
								</button>
								<button
									type="button"
									class="meditor__iconbtn"
									disabled={i === d[key]!.length - 1}
									aria-label="Move {label.toLowerCase()} {i + 1} down"
									onclick={() => movePerson(key, i, 1)}
								>
									<ChevronDown class="h-4 w-4" aria-hidden="true" />
								</button>
								<button
									type="button"
									class="meditor__iconbtn meditor__iconbtn--danger"
									aria-label="Remove {label.toLowerCase()} {i + 1}"
									onclick={() => removePerson(key, i)}
								>
									<Trash2 class="h-4 w-4" aria-hidden="true" />
								</button>
							</div>
						</li>
					{/each}
				</ul>
				<div class="meditor__list-actions">
					<Button variant="ghost" onclick={() => addPerson(key)}>Add</Button>
					<button
						type="button"
						class="meditor__clearbtn"
						class:is-on={isCleared(clearField)}
						onclick={() => toggleClear(clearField, true)}
					>
						Clear tag
					</button>
				</div>
			{/if}
			<p class="meditor__hint">
				The order here is the order in the output file and file name.
			</p>
		</fieldset>
	{/each}

	<!-- ── Series ──────────────────────────────────────────────── -->
	<fieldset class="meditor__block">
		<legend class="meditor__block-title">Series</legend>
		<div class="meditor__row meditor__row--two">
			<label class="meditor__field" for="meditor-series-name">
				<span class="meditor__field-label">Name</span>
				<span class="meditor__field-catalog">{snap.series_name || '—'}</span>
				<input
					id="meditor-series-name"
					class="meditor__input"
					type="text"
					bind:value={d.series_name}
					placeholder="Keep catalog series"
				/>
			</label>
			<label class="meditor__field" for="meditor-series-pos">
				<span class="meditor__field-label">Position</span>
				<span class="meditor__field-catalog">{snap.series_position || '—'}</span>
				<input
					id="meditor-series-pos"
					class="meditor__input"
					type="text"
					bind:value={d.series_position}
					placeholder="Keep catalog position"
				/>
			</label>
		</div>
		<div class="meditor__list-actions">
			<button
				type="button"
				class="meditor__clearbtn"
				class:is-on={isCleared('series')}
				onclick={() => toggleClear('series', true)}
			>
				Clear tag
			</button>
		</div>
	</fieldset>

	<!-- ── Description ─────────────────────────────────────────── -->
	<fieldset class="meditor__block">
		<legend class="meditor__block-title">Description</legend>
		<label class="meditor__field meditor__field--stacked" for="meditor-description">
			<span class="meditor__field-label">
				Description <span class="meditor__field-catalog">catalog: {snap.description ? 'present' : '—'}</span>
			</span>
			<textarea
				id="meditor-description"
				class="meditor__input meditor__input--area"
				rows="4"
				bind:value={d.description}
				placeholder="Keep catalog description"
			></textarea>
		</label>
		<div class="meditor__list-actions">
			<button
				type="button"
				class="meditor__clearbtn"
				class:is-on={isCleared('description')}
				onclick={() => toggleClear('description', true)}
			>
				Clear tag
			</button>
		</div>
	</fieldset>

	<!-- ── Genres ──────────────────────────────────────────────── -->
	<fieldset class="meditor__block">
		<legend class="meditor__block-title">Genres</legend>
		{#if d.genres === null}
			<p class="meditor__list-catalog">Catalog: {snap.genres.join(', ') || '—'}</p>
			<Button variant="secondary" onclick={() => ensureGenres()}>Edit list</Button>
		{:else}
			<ul class="meditor__chips" role="list">
				{#each d.genres as genre, i (i + ':' + genre)}
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
			<div class="meditor__list-actions meditor__list-actions--addrow">
				<input
					class="meditor__input meditor__input--add"
					type="text"
					placeholder="Add a genre, then Enter"
					onkeydown={addGenre}
					aria-label="Add a genre"
				/>
				<button
					type="button"
					class="meditor__clearbtn"
					class:is-on={isCleared('genres')}
					onclick={() => toggleClear('genres', true)}
				>
					Clear tag
				</button>
			</div>
		{/if}
	</fieldset>

	<!-- ── Year ────────────────────────────────────────────────── -->
	<fieldset class="meditor__block">
		<legend class="meditor__block-title">Year</legend>
		<div class="meditor__row">
			<label class="meditor__field" for="meditor-year">
				<span class="meditor__field-label">Year</span>
				<span class="meditor__field-catalog">{snap.year ?? '—'}</span>
			</label>
			<input
				id="meditor-year"
				class="meditor__input meditor__input--year"
				type="number"
				min="1000"
				max="2100"
				step="1"
				value={d.year ?? ''}
				onchange={(e) => {
					const v = (e.currentTarget as HTMLInputElement).value;
					d.year = v === '' ? null : Number(v);
				}}
				aria-label="Edited year, blank keeps the catalog year"
			/>
			<button
				type="button"
				class="meditor__clearbtn"
				class:is-on={isCleared('year')}
				onclick={() => toggleClear('year', true)}
			>
				Clear tag
			</button>
		</div>
	</fieldset>

	<!-- ── Cover ───────────────────────────────────────────────── -->
	<fieldset class="meditor__block">
		<legend class="meditor__block-title">Cover</legend>
		<div class="meditor__cover">
			<div class="meditor__cover-art">
				<CoverArt url={coverPreviewUrl} title={snap.title} size="sm" />
			</div>
			<div class="meditor__cover-opts">
				<label class="meditor__radio">
					<input
						type="radio"
						name="meditor-cover"
						checked={d.cover_source === ''}
						onchange={() => setCoverSource('')}
					/>
					Catalog cover
				</label>
				<label class="meditor__radio">
					<input
						type="radio"
						name="meditor-cover"
						checked={d.cover_source === 'file'}
						onchange={() => setCoverSource('file')}
					/>
					Upload artwork
				</label>
				<label class="meditor__radio">
					<input
						type="radio"
						name="meditor-cover"
						checked={d.cover_source === 'url'}
						onchange={() => setCoverSource('url')}
					/>
					Cover URL
				</label>
				<label class="meditor__radio">
					<input
						type="radio"
						name="meditor-cover"
						checked={d.cover_source === 'clear'}
						onchange={() => setCoverSource('clear')}
					/>
					Remove cover from output
				</label>
				{#if d.cover_source === 'file'}
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
				{#if d.cover_source === 'url'}
					<input
						class="meditor__input"
						type="url"
						bind:value={d.cover_url}
						placeholder="https://…/cover.jpg"
						aria-label="Cover URL"
					/>
				{/if}
			</div>
		</div>
		<p class="meditor__hint">
			Uploaded artwork is stored as a fixed file — re-uploading makes a new asset and never
			rewrites one a queued job already names.
		</p>
	</fieldset>

	<!-- ── Chapters ────────────────────────────────────────────── -->
	<fieldset class="meditor__block">
		<legend class="meditor__block-title">Chapters</legend>
		{#if !isEditedChapters}
			<p class="meditor__list-catalog">
				Catalog: {snap.chapters?.length ?? 0} chapter{(snap.chapters?.length ?? 0) === 1 ? '' : 's'}
			</p>
			{#if (snap.chapters?.length ?? 0) > 0}
				<details class="meditor__catalog-chapters">
					<summary>Show catalog chapters</summary>
					<ol class="meditor__catalog-list">
						{#each snap.chapters as c (c.title + c.start_millis)}
							<li>
								{c.title}
								<span class="meditor__catalog-time">{millisHuman(c.start_millis)} · {millisHuman(c.duration_millis)}</span>
							</li>
						{/each}
					</ol>
				</details>
			{/if}
			<div class="meditor__list-actions">
				<Button variant="secondary" onclick={() => (d.chapters = [])}>Replace with custom chapters</Button>
			</div>
		{:else}
			{#if d.chapters!.length === 0}
				<p class="meditor__list-catalog meditor__list-catalog--warn">
					No chapters — the chapters tag is removed from the output.
				</p>
			{/if}
			<ul class="meditor__chapters" role="list">
				{#each d.chapters as c, i (i + ':' + c.title + c.start_millis)}
					<li class="meditor__chapter">
						<span class="meditor__chapter-order">{i + 1}</span>
						<input
							class="meditor__input meditor__chapter-title"
							type="text"
							value={c.title}
							placeholder="Chapter title"
							aria-label="Chapter {i + 1} title"
							onchange={(e) => {
								const list = [...d!.chapters!];
								list[i] = { ...list[i], title: (e.currentTarget as HTMLInputElement).value };
								d.chapters = list;
							}}
						/>
						<label class="meditor__chapter-time">
							<span>Start (ms)</span>
							<input
								class="meditor__input meditor__input--millis"
								type="number"
								min="0"
								step="1"
								value={c.start_millis}
								aria-label="Chapter {i + 1} start in milliseconds"
								onchange={(e) => {
									const list = [...d!.chapters!];
									list[i] = { ...list[i], start_millis: Number((e.currentTarget as HTMLInputElement).value) };
									d.chapters = list;
								}}
							/>
						</label>
						<label class="meditor__chapter-time">
							<span>Duration (ms)</span>
							<input
								class="meditor__input meditor__input--millis"
								type="number"
								min="0"
								step="1"
								value={c.duration_millis}
								aria-label="Chapter {i + 1} duration in milliseconds"
								onchange={(e) => {
									const list = [...d!.chapters!];
									list[i] = { ...list[i], duration_millis: Number((e.currentTarget as HTMLInputElement).value) };
									d.chapters = list;
								}}
							/>
						</label>
						<span class="meditor__chapter-human" aria-hidden="true">
							{millisHuman(c.start_millis)} · {millisHuman(c.duration_millis)}
						</span>
						<div class="meditor__people-actions">
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
								disabled={i === d.chapters!.length - 1}
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
			</ul>
			{#if chapterErrors.length}
				<ul class="meditor__chapter-errors" role="alert">
					{#each chapterErrors as problem}
						<li>{problem}</li>
					{/each}
				</ul>
			{/if}
			<div class="meditor__list-actions">
				<Button variant="ghost" onclick={addChapter}>Add chapter</Button>
				<button type="button" class="meditor__clearbtn" onclick={() => (d.chapters = [])}>
					No chapters (clear tag)
				</button>
				<button type="button" class="meditor__clearbtn" onclick={useCatalogChapters}>
					Use catalog chapters
				</button>
			</div>
		{/if}
	</fieldset>

	<!-- ── Commit row ──────────────────────────────────────────── -->
	<div class="meditor__commit">
		{#if confirmingReset}
			<div class="meditor__confirm">
				<span>Discard all manual edits and use the catalog values again? (Your chosen release is kept.)</span>
				<Button variant="danger" loading={resetting} disabled={uploading} onclick={doReset}>Yes, reset</Button>
				<Button variant="ghost" onclick={() => (confirmingReset = false)}>Cancel</Button>
			</div>
		{:else}
			<div class="meditor__commit-btns">
				<!-- Save/reset both run the server's cover-prune sweep; an
				 in-flight upload's asset is not referenced by the saved row
				 yet, so commits are blocked until the upload settles. -->
				<Button
					variant="primary"
					loading={saving}
					disabled={chapterErrors.length > 0 || uploading}
					onclick={save}
				>
					Save manual metadata
				</Button>
				<Button variant="ghost" disabled={uploading} onclick={() => (confirmingReset = true)}>
					Reset to catalog
				</Button>
			</div>
			<p class="meditor__commit-note">
				Saving records the edit in the database. Tags are written to the output file the
				next time a processing job runs.
			</p>
		{/if}
	</div>

	{#if saveError}
		<Alert variant="error">
			{saveError.message}
			<ErrorDetail raw={saveError.raw} needsSettings={saveError.needsSettings} />
		</Alert>
	{:else if savedNote}
		<Alert variant="success" role="status">Saved.</Alert>
	{/if}
{/if}

<style>
	.meditor__loading {
		padding: 1rem 0;
		color: var(--text-muted);
	}

	/* ── Naming preview ── */
	.meditor__preview {
		display: flex;
		align-items: baseline;
		gap: 0.75rem;
		padding: 0.625rem 0.875rem;
		margin-bottom: 1rem;
		background: var(--surface);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		flex-wrap: wrap;
	}

	.meditor__preview-label {
		font-size: 12px;
		font-weight: 600;
		color: var(--text-secondary);
		text-transform: uppercase;
		letter-spacing: 0.05em;
		flex-shrink: 0;
	}

	.meditor__preview-path {
		font-size: 13px;
		overflow-wrap: anywhere;
	}

	.meditor__preview-muted {
		color: var(--text-muted);
		font-size: 13.5px;
	}

	/* ── Blocks ── */
	.meditor__block {
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-sm);
		padding: 0.875rem 1rem 1rem;
		margin: 0 0 1rem;
	}

	.meditor__block-title {
		font-size: 14px;
		font-weight: 650;
		padding: 0 0.375rem;
		color: var(--text);
	}

	/* ── Rows: label column + input + clear ── */
	.meditor__row {
		display: grid;
		grid-template-columns: minmax(0, 14rem) minmax(0, 1fr) auto;
		align-items: center;
		gap: 0.75rem;
	}

	@media (max-width: 720px) {
		.meditor__row {
			grid-template-columns: minmax(0, 1fr) auto;
		}

		.meditor__row .meditor__field-catalog {
			display: none;
		}
	}

	.meditor__row--two {
		grid-template-columns: 1fr 1fr;
	}

	@media (max-width: 560px) {
		.meditor__row--two {
			grid-template-columns: 1fr;
		}
	}

	.meditor__field {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		grid-template-rows: auto auto;
		gap: 0 0.75rem;
		align-items: center;
		min-width: 0;
	}

	.meditor__field--stacked {
		grid-template-columns: 1fr;
	}

	.meditor__field-label {
		font-size: 14px;
		font-weight: 600;
		grid-column: 1;
		grid-row: 1 / 3;
	}

	.meditor__field-catalog {
		font-size: 13px;
		color: var(--text-muted);
		grid-column: 2;
		grid-row: 1;
		overflow-wrap: anywhere;
		text-align: right;
	}

	.meditor__field--stacked .meditor__field-catalog {
		grid-column: 1;
		grid-row: 2;
		text-align: left;
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

	.meditor__input--area {
		min-height: 6rem;
		resize: vertical;
		grid-column: 1;
		grid-row: 3;
	}

	.meditor__input--person {
		flex: 1;
	}

	.meditor__input--add {
		flex: 1;
	}

	/* ── Clear-tag button (the explicit tag-removal control) ── */
	.meditor__clearbtn {
		min-height: 32px;
		padding: 0 0.75rem;
		font-size: 13px;
		font-weight: 600;
		font-family: inherit;
		color: var(--text-secondary);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		cursor: pointer;
		white-space: nowrap;
	}

	.meditor__clearbtn:hover:not(:disabled) {
		background: var(--surface-hover);
		border-color: var(--border-strong);
	}

	.meditor__clearbtn:focus-visible {
		outline: 3px solid var(--focus-ring);
		outline-offset: 1px;
	}

	.meditor__clearbtn:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}

	.meditor__clearbtn.is-on {
		color: var(--state-red);
		background: var(--state-red-bg);
		border-color: var(--state-red-border);
	}

	/* ── Hints ── */
	.meditor__hint {
		margin: 0.5rem 0 0;
		font-size: 13px;
		color: var(--text-muted);
	}

	/* ── People & genres lists ── */
	.meditor__list-catalog {
		margin: 0 0 0.625rem;
		font-size: 14px;
		color: var(--text-secondary);
	}

	.meditor__list-catalog--warn {
		color: var(--state-amber);
	}

	.meditor__people {
		list-style: none;
		margin: 0 0 0.625rem;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
	}

	.meditor__people-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
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
	}

	.meditor__iconbtn {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 32px;
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
		padding: 0.125rem;
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

	/* ── Cover ── */
	.meditor__cover {
		display: flex;
		gap: 1rem;
		align-items: flex-start;
		flex-wrap: wrap;
	}

	.meditor__cover-art {
		flex-shrink: 0;
	}

	.meditor__cover-opts {
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
		flex: 1;
		min-width: 12rem;
	}

	.meditor__radio {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 14px;
		min-height: 28px;
		cursor: pointer;
	}

	.meditor__radio input {
		width: 16px;
		height: 16px;
		accent-color: var(--workspace-action);
		margin: 0;
	}

	.meditor__cover-file {
		display: flex;
		align-items: center;
		gap: 0.625rem;
		flex-wrap: wrap;
		padding-left: 1.5rem;
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
	}

	/* ── Chapters ── */
	.meditor__catalog-chapters {
		margin-bottom: 0.625rem;
	}

	.meditor__catalog-chapters summary {
		font-size: 13px;
		font-weight: 600;
		color: var(--workspace-action);
		cursor: pointer;
		min-height: 24px;
	}

	.meditor__catalog-list {
		margin: 0.375rem 0 0;
		padding-left: 1.25rem;
		font-size: 13px;
		color: var(--text-secondary);
		display: flex;
		flex-direction: column;
		gap: 0.125rem;
	}

	.meditor__catalog-time {
		color: var(--text-muted);
		font-variant-numeric: tabular-nums;
	}

	.meditor__chapters {
		list-style: none;
		margin: 0 0 0.625rem;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.meditor__chapter {
		display: grid;
		grid-template-columns: 1.25rem minmax(0, 1.4fr) auto auto auto;
		align-items: center;
		gap: 0.5rem;
	}

	@media (max-width: 860px) {
		.meditor__chapter {
			grid-template-columns: 1.25rem minmax(0, 1fr) auto;
		}

		.meditor__chapter-human {
			display: none;
		}
	}

	.meditor__chapter-order {
		text-align: center;
		font-size: 12px;
		font-weight: 600;
		color: var(--text-muted);
		font-variant-numeric: tabular-nums;
	}

	.meditor__chapter-title {
		min-width: 0;
	}

	.meditor__chapter-time {
		display: grid;
		grid-template-columns: auto 1fr;
		align-items: center;
		gap: 0.375rem;
		font-size: 12px;
		font-weight: 600;
		color: var(--text-muted);
		white-space: nowrap;
	}

	.meditor__chapter-human {
		font-size: 12px;
		color: var(--text-muted);
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
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

	/* ── Commit row ── */
	.meditor__commit {
		border-top: 1px solid var(--border-subtle);
		padding-top: 1rem;
		margin-top: 0.25rem;
	}

	.meditor__commit-btns {
		display: flex;
		align-items: center;
		gap: 0.625rem;
		flex-wrap: wrap;
	}

	.meditor__commit-note {
		margin: 0.5rem 0 0;
		font-size: 13px;
		color: var(--text-muted);
		max-width: 60ch;
	}

	.meditor__confirm {
		display: flex;
		align-items: center;
		gap: 0.625rem;
		flex-wrap: wrap;
		font-size: 14px;
	}
</style>
