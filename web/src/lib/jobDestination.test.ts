import { describe, expect, it } from 'vitest';
import { createJobLogTracker, jobStage, reportedDestination } from './jobDestination';

const ID = '97e25943-e035-4ae8-b139-d293e48c3833';
const TMP = `/erotica/${ID}.tmp`;
const FINAL = '/erotica/Annabelle Hawthorne/Fertile Valley 1/Fertile Valley 1.m4b';

describe('reportedDestination', () => {
	it('reads the merge target and strips the per-job working segment', () => {
		const output = `2026-10-28T18:42:11.617350Z  INFO m4b_merge::merge: Starting merge: 1 files -> ${TMP}/Annabelle Hawthorne/Fertile Valley 1/Fertile Valley 1.m4b (mode: Copy, bitrate: None)\n`;
		expect(reportedDestination(output, ID)).toBe(FINAL);
	});

	it('reads a success line that has no arrow', () => {
		const output = `2026-10-28T18:42:15.945481Z  INFO m4b_merge::processor: Successfully merged to: ${TMP}/Annabelle Hawthorne/Fertile Valley 1/Fertile Valley 1.m4b\n`;
		expect(reportedDestination(output, ID)).toBe(FINAL);
	});

	it('ignores later lines that mention the same working path but are not the audio file', () => {
		const output = [
			`INFO m4b_merge::merge: Starting merge: 1 files -> ${TMP}/A Bunyan/Big Book/Big Book.m4b (mode: Copy, bitrate: None)`,
			`INFO m4b_merge::tagging: Successfully wrote chapters.txt to: ${TMP}/A Bunyan/Big Book/Big Book.chapters.txt`
		].join('\n');
		expect(reportedDestination(output, ID)).toBe('/erotica/A Bunyan/Big Book/Big Book.m4b');
	});

	it('keeps spaces in the path', () => {
		const output = `INFO m4b_merge::merge: Starting merge: 1 files -> ${TMP}/Ursula K. Le Guin/The Left Hand of Darkness/The Left Hand of Darkness.m4b (mode: Copy, bitrate: None)`;
		expect(reportedDestination(output, ID)).toBe(
			'/erotica/Ursula K. Le Guin/The Left Hand of Darkness/The Left Hand of Darkness.m4b'
		);
	});

	it('returns empty before the engine reports a path or for another job', () => {
		expect(reportedDestination('INFO m4b-merge starting with 1 input(s)\n', ID)).toBe('');
		const other = '00000000-0000-4000-8000-000000000000';
		expect(
			reportedDestination(`INFO Starting merge: 1 files -> /out/${other}.tmp/A/B/B.m4b (mode: Copy)`, ID)
		).toBe('');
	});
});

describe('createJobLogTracker', () => {
	const mergeLines = [
		`INFO m4b_merge::merge: Starting merge: 1 files -> ${TMP}/Annabelle Hawthorne/Fertile Valley 1/Fertile Valley 1.m4b (mode: Copy, bitrate: None)`,
		'[0/1] [Discovery] Discovering audio files...',
		'[0/1] [Merging] Merging audio files...',
		'[0/1] [Tagging] Writing metadata and cover art...',
	];
	const mergeOutput = mergeLines.join('\n');

	it('returns the full-log derivations for an initial snapshot', () => {
		const tracker = createJobLogTracker(ID, mergeOutput);
		expect(tracker.stage()).toBe('Writing metadata and cover');
		expect(tracker.destination()).toBe(FINAL);
	});

	it('updates derivations from appended suffix lines only', () => {
		const tracker = createJobLogTracker(ID, mergeLines.slice(0, 3).join('\n'));
		expect(tracker.stage()).toBe('Merging audio');
		expect(tracker.destination()).toBe(FINAL);
		for (const line of mergeLines.slice(3)) {
			tracker.append(line + '\n');
			expect(tracker.stage()).toEqual(jobStage(mergeOutput));
			expect(tracker.destination()).toEqual(reportedDestination(mergeOutput, ID));
		}
	});

	it('tracks progress byte-wise across appends that split an annotated line', () => {
		// Engine-terminated log: every line, seed included, ends in '\n'.
		const full = mergeOutput + '\n';
		const seedLine = mergeLines[0] + '\n';
		const rest = full.slice(seedLine.length);
		const cut = rest.slice(0, 12); // ends mid-line, INSIDE the '[Discovery]' marker
		const tracker = createJobLogTracker(ID, seedLine);
		// First suffix ends mid-line: the incomplete '[0/1] [Disc…' must not
		// announce a stage yet (an unfinished tail is held, not committed)…
		tracker.append(cut);
		expect(tracker.stage()).toBe('');
		expect(tracker.destination()).toBe(FINAL);
		// …and the second suffix completes the marker and the log.
		tracker.append(rest.slice(12));
		// Byte-for-byte parity with the full-re-scan derivations.
		expect(tracker.stage()).toBe(jobStage(full));
		expect(tracker.destination()).toBe(reportedDestination(full, ID));
	expect(tracker.stage()).toBe('Writing metadata and cover');
	expect(tracker.destination()).toBe(FINAL);
	// An incomplete tail that ALREADY CONTAINS A COMPLETE marker: the held-
	// tail guard must not commit 'Merging audio' until the line ends — an
	// eager scan of the tail would flip the cached stage right here, while
	// the earlier cut ('[0/1] [Disc…') matches no marker and cannot tell the
	// two scans apart.
	tracker.append('[0/1] [Merging] Merging audio');
	expect(tracker.stage()).toBe('Writing metadata and cover');
	// The newline completes the line and commits the marker…
	tracker.append(' files...\n');
	// …and parity now covers the extended log.
	const finalFull = full + '[0/1] [Merging] Merging audio files...\n';
	expect(tracker.stage()).toBe(jobStage(finalFull));
	expect(tracker.destination()).toBe(reportedDestination(finalFull, ID));
	expect(tracker.stage()).toBe('Merging audio');
	expect(tracker.destination()).toBe(FINAL);
	});

	it('keeps following a later preferred line and matches the full re-scan', () => {
		const tracker = createJobLogTracker(ID, '');
		tracker.append(`${mergeLines[0]}\n`);
		expect(tracker.destination()).toBe(FINAL);
		// A later merge to a different target wins (newest preferred line).
		tracker.append(`INFO m4b_merge::merge: Starting merge: 1 files -> ${TMP}/Ursula K. Le Guin/The Left Hand of Darkness/The Left Hand of Darkness.m4b (mode: Copy, bitrate: None)\n`);
		expect(tracker.destination()).toBe('/erotica/Ursula K. Le Guin/The Left Hand of Darkness/The Left Hand of Darkness.m4b');
		// Non-preferred lines that mention the working path don't displace it.
		tracker.append(`INFO moving cover: ${TMP}/Annabelle Hawthorne/Fertile Valley 1/cover.jpg\n`);
		const full = [
			mergeLines[0],
			`INFO m4b_merge::merge: Starting merge: 1 files -> ${TMP}/Ursula K. Le Guin/The Left Hand of Darkness/The Left Hand of Darkness.m4b (mode: Copy, bitrate: None)`,
			`INFO moving cover: ${TMP}/Annabelle Hawthorne/Fertile Valley 1/cover.jpg`,
		].join('\n') + '\n';
		expect(tracker.destination()).toBe(reportedDestination(full, ID));
	});

	it('falls back to a non-preferred reported path when no preferred line exists', () => {
		const tracker = createJobLogTracker(ID, '');
		const full = `INFO m4b_merge::tagging: Big Book.m4b written to: ${TMP}/A Bunyan/Big Book/Big Book.m4b\n`;
		expect(tracker.destination()).toBe('');
		tracker.append(full);
		expect(tracker.destination()).toBe('/erotica/A Bunyan/Big Book/Big Book.m4b');
		expect(tracker.destination()).toBe(reportedDestination(full, ID));
	});

	it('restarts the cache from a fresh snapshot when the log is replaced', () => {
		const tracker = createJobLogTracker(ID, mergeOutput);
		expect(tracker.stage()).toBe('Writing metadata and cover');
		tracker.replace('INFO starting over\n[0/1] [Discovery] Discovering audio files...\n');
		expect(tracker.stage()).toBe('Discovering audio files');
		expect(tracker.destination()).toBe('');
	});

	it('never commits a marker split across suffixes until the line ends', () => {
		// Fully-scanned seed: the first stage() folds the whole log in, so
		// every later append starts a held (unscanned) tail.
		const tracker = createJobLogTracker(ID, 'INFO starting merge\n');
		tracker.stage();
		const previousStage = tracker.stage();
		const previousDestination = tracker.destination();

		// An EMPTY suffix must not disturb the caches of a fully-scanned log.
		tracker.append('');
		expect(tracker.stage()).toBe(previousStage);
		expect(tracker.destination()).toBe(previousDestination);

		// A tail that ends mid-marker ('[0/1] [Disc…') is held — no stage
		// may be read out of an unfinished line.
		tracker.append('[0/1] [Disc');
		expect(tracker.stage()).toBe(previousStage);
		expect(tracker.destination()).toBe(previousDestination);

		// The newline completes the line: the marker commits as a whole, in
		// parity with the full re-scan of the concatenated log.
		tracker.append('overy] Discovering audio files...\n');
		const full = 'INFO starting merge\n[0/1] [Discovery] Discovering audio files...\n';
		expect(tracker.stage()).toBe('Discovering audio files');
		expect(tracker.stage()).toBe(jobStage(full));
		expect(tracker.destination()).toBe(reportedDestination(full, ID));
	});
});

describe('jobStage', () => {
	it('returns the latest announced stage', () => {
		const output = [
			'[0/1] [Discovery] Discovering audio files...',
			'[0/1] [API Lookup] Fetching metadata for ASIN: B0DNRN1H54...',
			'[0/1] [Merging] Merging audio files...',
			'[0/1] [Tagging] Writing metadata and cover art...',
			'[0/1] [Moving Files] Moving source files to completed directory...'
		].join('\n');
		expect(jobStage(output)).toBe('Moving source files');
	});

	it('recognises the completion marker', () => {
		expect(jobStage('=== Processing Complete ===\n')).toBe('Finishing');
	});

	it('returns empty before the first marker', () => {
		expect(jobStage('INFO m4b-merge starting with 1 input(s)\n')).toBe('');
	});
});
