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
