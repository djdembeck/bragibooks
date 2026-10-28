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

/** The stage m4b-merge most recently announced, or '' before the first marker. */
export function jobStage(output: string): string {
	let stage = '';
	for (const line of output.split('\n')) {
		for (const marker of STAGE_MARKERS) {
			if (marker.pattern.test(line)) stage = marker.label;
		}
	}
	return stage;
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
	const marker = `/${jobID}.tmp/`;
	const preferred = ['Starting merge:', 'Successfully merged to:'];
	const lines = output.split('\n');

	for (let pass = 0; pass < 2; pass++) {
		for (let i = lines.length - 1; i >= 0; i--) {
			const line = lines[i];
			if (!line.includes(marker)) continue;
			if (pass === 0 && !preferred.some((prefix) => line.includes(prefix))) continue;
			const path = pathFromLine(line, marker);
			if (path && /\.m4b$/i.test(path)) return path;
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
