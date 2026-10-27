/**
 * Job timestamps come from SQLite `datetime('now')`, which writes UTC as
 * `YYYY-MM-DD HH:MM:SS` with no zone marker. ECMAScript parses an
 * offset-less date-time as browser-local, so it must be re-labelled as UTC
 * before formatting or the UTC wall clock is shown as if it were local.
 */
const NAIVE_UTC = /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?$/;

export function parseJobTimestamp(value: string): Date | null {
	const ts = value.trim();
	if (!ts) return null;
	const normalized = NAIVE_UTC.test(ts) ? `${ts.replace(' ', 'T')}Z` : ts;
	const date = new Date(normalized);
	return Number.isNaN(date.getTime()) ? null : date;
}

const formatter = new Intl.DateTimeFormat(undefined, {
	dateStyle: 'medium',
	timeStyle: 'medium'
});

/** Locale-aware display of a stored job timestamp in the viewer's time zone. */
export function formatJobTime(value: string | null | undefined): string {
	if (!value) return '—';
	const date = parseJobTimestamp(value);
	return date ? formatter.format(date) : value;
}

/**
 * Human duration between two stored job timestamps. Terminal jobs keep the
 * seconds they showed while running up to an hour (`12s`, `5m 0s`); longer
 * ones step to hours and minutes (`27h 14m`). A null `ended` means the job is
 * still running, so the second endpoint is `nowMs`.
 *
 * Both endpoints share the naive-UTC source, so the delta is independent of
 * the viewer's time zone. Negative deltas clamp to zero to absorb clock skew;
 * an unparseable endpoint yields an em dash so the field never renders blank.
 */
export function formatElapsed(since: string | null, ended: string | null, nowMs: number): string {
	const start = since ? parseJobTimestamp(since) : null;
	if (!start) return '—';

	const live = ended == null;
	const end = ended == null ? null : parseJobTimestamp(ended);
	if (ended != null && !end) return '—';

	const ms = Math.max(0, (end ? end.getTime() : nowMs) - start.getTime());
	const sec = Math.floor(ms / 1000);
	const h = Math.floor(sec / 3600);
	const m = Math.floor((sec % 3600) / 60);
	const s = sec % 60;

	if (live) {
		if (h) return `${h}h ${m}m ${s}s`;
		if (m) return `${m}m ${s}s`;
		return `${s}s`;
	}
	if (h) return `${h}h ${m}m`;
	if (m) return `${m}m ${s}s`;
	return `${s}s`;
}

/**
 * The clock a duration describes: queue wait before a job starts, then
 * processing time. `started_at` is the discriminator, so a job cancelled
 * while queued reports the wait it actually had.
 */
export function elapsedLabel(startedAt: string | null | undefined): 'Elapsed' | 'Queued' {
	return startedAt ? 'Elapsed' : 'Queued';
}
