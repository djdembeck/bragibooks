import { describe, expect, it } from 'vitest';
import { jobStage, reportedDestination } from './jobDestination';

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
