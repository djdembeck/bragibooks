/**
 * Read the live job log for the two facts the Queue card shows while a run is
 * in flight: the destination m4b-merge actually resolved, and the current
 * stage. The engine prints both; nothing is inferred from the saved metadata.
 */

/** m4b-merge's bracketed stage markers, latest match wins. */
const STAGE_MARKERS: Array<{ pattern: RegExp; label: string }> = [
	{ pattern: /\[Discovery\]/, label: 'Discovering audio files' },
	{ pattern: /\[API Lookup\]|Fetching metadata for ASIN/, label: 'Fetching metadata' },
	{ pattern: /\[Merging\]/, label: 'Merging audio' },
	{ pattern: /\[Tagging\]/, label: 'Writing metadata and cover' },
	{ pattern: /\[Moving Files\]/, label: 'Moving source files' },
	{ pattern: /Processing Complete/, label: 'Finishing' }
];

/** The stage a single line announces, or '' when it announces none. */
function stageFromLine(line: string): string {
	let stage = '';
	for (const marker of STAGE_MARKERS) {
		if (marker.pattern.test(line)) stage = marker.label;
	}
	return stage;
}

/** The stage m4b-merge most recently announced, or '' before the first marker. */
export function jobStage(output: string): string {
	let stage = '';
	for (const line of output.split('\n')) {
		const announced = stageFromLine(line);
		if (announced) stage = announced;
	}
	return stage;
}

/**
 * The .m4b path a single line reports for this job — either the `-> <path>
 * (mode: …)` merge target (when `preferredOnly`) or any `... : <path>` line —
 * with the per-job `.tmp` working segment removed. Empty when the line
 * reports no usable path for this job.
 */
function destinationFromLine(line: string, jobID: string, preferredOnly: boolean): string {
	const marker = `/${jobID}.tmp/`;
	if (!line.includes(marker)) return '';
	if (preferredOnly && !line.includes('Starting merge:') && !line.includes('Successfully merged to:')) return '';
	const path = pathFromLine(line, marker);
	if (path && /\.m4b$/i.test(path)) return path;
	return '';
}

/**
 * The final .m4b path reported by m4b-merge for this run, with the per-job
 * `.tmp` working segment removed. Empty until the engine logs the path.
 *
 * Paths can contain spaces, so the path is read from the line shape, not by
 * splitting on whitespace: either the `-> <path> (mode: …)` merge target or a
 * `Successfully merged to: <path>` line. Later lines that also mention the
 * working path (cover art, chapters.txt) are ignored by the prefix filter.
 */
export function reportedDestination(output: string, jobID: string): string {
	if (!output || !jobID) return '';
	const lines = output.split('\n');
	for (let pass = 0; pass < 2; pass++) {
		for (let i = lines.length - 1; i >= 0; i--) {
			const path = destinationFromLine(lines[i], jobID, pass === 0);
			if (path) return path;
		}
	}
	return '';
}

function pathFromLine(line: string, marker: string): string {
	let rest = '';
	const arrow = line.lastIndexOf(' -> ');
	if (arrow >= 0) {
		rest = line.slice(arrow + 4);
	} else {
		const colon = line.lastIndexOf(': ');
		if (colon < 0) return '';
		rest = line.slice(colon + 2);
	}
	const mode = rest.indexOf(' (mode:');
	if (mode >= 0) rest = rest.slice(0, mode);
	rest = rest.trim();
	if (!rest.startsWith('/') || !rest.includes(marker)) return '';
	return rest.replace(/\/{2,}/g, '/').replace(marker, '/');
}

export interface JobLogTracker {
	/** Replace the log with a fresh full snapshot (connect/reconnect payload). */
	replace(output: string): void;
	/** Append the bytes of one incremental suffix event. */
	append(suffix: string): void;
	/** The stage m4b-merge most recently announced (cached; new lines only re-scan). */
	stage(): string;
	/** The .m4b destination announced for this job (cached; new lines only re-scan). */
	destination(): string;
}

/**
 * Per-job cache over a streaming log. The SSE feed is a full snapshot plus
 * suffixes, and a long conversion's log keeps growing, so each suffix event
 * must re-split ONLY the lines it added — not the accumulated log. The
 * derivations are incremental exactly as the engine prints them: stage is
 * "latest marker wins" and the destination is the latest reported path from
 * the newest matching lines, so scanning forward is equivalent to a full
 * re-scan.
 */
export function createJobLogTracker(jobID: string, initialOutput = ''): JobLogTracker {
	let output = initialOutput;
	// String index of the first byte not yet folded into the caches below.
	let scanned = 0;
	let stageCache = '';
	let preferredPath = '';
	let fallbackPath = '';
	// The SSE contract admits partial chunks: a suffix may end mid-line, and
	// a marker whose bytes are split across two suffixes must still be seen.
	// A full-log seed (constructor/replace) is complete and is scanned whole;
	// appended tails are only scanned up to their last '\n', and the
	// remainder is re-examined on the next append.
	let partialTail = false;

	function ensureScanned() {
		if (scanned >= output.length) return;
		// The last '\n' in the whole string is the last one in the unscanned
		// tail (scanning always stops at a '\n'); when the tail has none, the
		// hit falls back to the boundary '\n' already consumed (scanned - 1),
		// which yields no new scan range.
		const boundary = partialTail ? output.lastIndexOf('\n') + 1 : output.length;
		if (boundary < scanned + 1) {
			// No newline in the tail yet (or a lone first tail char); the
			// unfinished line is re-examined when more bytes arrive.
			return;
		}
		for (const line of output.slice(scanned, boundary).split('\n')) {
			const announced = stageFromLine(line);
			if (announced) stageCache = announced;
			const preferred = destinationFromLine(line, jobID, true);
			if (preferred) {
				preferredPath = preferred;
			} else {
				const fallback = destinationFromLine(line, jobID, false);
				if (fallback) fallbackPath = fallback;
			}
		}
		scanned = boundary;
	}

	return {
		replace(next: string) {
			output = next;
			scanned = 0;
			stageCache = '';
			preferredPath = '';
			fallbackPath = '';
			// A snapshot is a complete log: the next scan may read to the end.
			partialTail = false;
		},
		append(suffix: string) {
			output += suffix;
			partialTail = true;
		},
		stage() {
			ensureScanned();
			return stageCache;
		},
		destination() {
			ensureScanned();
			return preferredPath || fallbackPath;
		}
	};
}
