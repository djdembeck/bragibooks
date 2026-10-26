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
 * Human duration between two stored job timestamps, e.g. `12s`, `5m`,
 * `27h 14m`, or live `1h 4m 30s`. `ended` null means "still running" and the
 * second endpoint is `nowMs`. The delta is offset-invariant — both endpoints
 * come from the same naive-UTC source — so it is correct even before #442
 * fixes absolute clock rendering. Clamps to zero to hide any clock skew.
 */
export function formatElapsed(since: string | null, ended: string | null, nowMs: number): string {
	const start = since ? parseJobTimestamp(since) : null;
	if (!start) return '';
	const live = !ended;
	const end = live ? nowMs : ended ? parseJobTimestamp(ended)?.getTime() ?? NaN : NaN;
	if (!live && Number.isNaN(end)) return '';
	const ms = Math.max(0, (live ? nowMs : end) - start.getTime());
	const sec = Math.floor(ms / 1000);
	const h = Math.floor(sec / 3600);
	const m = Math.floor((sec % 3600) / 60);
	const s = sec % 60;
	if (live) {
		const parts = [h ? `${h}h` : '', m ? `${m}m` : '', s ? `${s}s` : ''];
		return parts.filter(Boolean).join(' ');
	}
	if (sec < 60) return `${s}s`;
	if (sec < 3600) return `${m}m`;
	return `${h}h ${m}m`;
}
