/**
 * Canonical status kinds shared by every readout on the bench.
 * Raw book/job statuses arrive as several spellings — normalize once, here.
 */
export type StatusKind = 'pending' | 'ready' | 'processing' | 'done' | 'error'

/** pending ◇ graphite · ready ▣ · processing ◐ amber · done ◆ green · error ✕ red */
export function normalizeStatus(status: string): StatusKind {
	const s = status.toLowerCase()
	if (s === 'done' || s === 'completed') return 'done'
	if (s === 'processing' || s === 'running' || s === 'queued') return 'processing'
	if (s === 'error' || s === 'failed') return 'error'
	if (s === 'ready' || s === 'matched') return 'ready'
	return 'pending'
}
