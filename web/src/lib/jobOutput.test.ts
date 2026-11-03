import { describe, expect, it } from 'vitest';
import { mergeOutputEvent, type MergedOutput, type OutputEvent, type OutputState } from './jobOutput';

describe('mergeOutputEvent', () => {
	type Case = [string, OutputState, OutputEvent, MergedOutput];

	const cases: Case[] = [
		[
			'fresh job: the initial snapshot (offset only, no output field) starts an empty log at the server cursor',
			{ output: '', offset: null },
			{ output_offset: 18 },
			{ output: '', offset: 18, action: 'replace' }
		],
		[
			'snapshot with accumulated output replaces the item log and sets the cursor',
			{ output: 'stale\n', offset: null },
			{ output: 'line one\nline two\n', output_offset: 18 },
			{ output: 'line one\nline two\n', offset: 18, action: 'replace' }
		],
		[
			'suffix beyond the cursor appends and advances the cursor',
			{ output: 'a\n', offset: 18 },
			{ output: 'next line\n', output_offset: 28 },
			{ output: 'a\nnext line\n', offset: 28, action: 'append' }
		],
		[
			'empty-suffix nudge (offset equal to the cursor) leaves the log unchanged',
			{ output: 'a\nb\n', offset: 28 },
			{ output_offset: 28 },
			{ output: 'a\nb\n', offset: 28, action: 'ignore' }
		],
		[
			'replay of already-logged bytes (offset below the cursor) is ignored',
			{ output: 'a\nb\n', offset: 28 },
			{ output: 'a\n', output_offset: 12 },
			{ output: 'a\nb\n', offset: 28, action: 'ignore' }
		],
		[
			'offset-less payload is a full snapshot: replaces and resets the cursor',
			{ output: 'old log\n', offset: 44 },
			{ output: 'full\nlog\nfrom\nthe\nDB\n' },
			{ output: 'full\nlog\nfrom\nthe\nDB\n', offset: null, action: 'replace' }
		],
		[
			'payload with no output fields at all is untouched',
			{ output: 'a\n', offset: 18 },
			{},
			{ output: 'a\n', offset: 18, action: 'ignore' }
		]
	];

	it.each(cases)('%s', (_name, state, event, want) => {
		expect(mergeOutputEvent(state, event)).toEqual(want);
	});

	it('reconnect: the second snapshot replaces the appended log with no doubled prefix', () => {
		// First connection: resolved snapshot at offset 18, one suffix after.
		let state: OutputState = { output: '', offset: null };
		state = mergeOutputEvent(state, { output: 'a\nb\nc\n', output_offset: 18 });
		state = mergeOutputEvent(state, { output: 'tail\n', output_offset: 61 });
		expect(state).toEqual({ output: 'a\nb\nc\ntail\n', offset: 61, action: 'append' });

		// Connection drops; the next one hands the cursor back to the
		// replace branch (the handler resets item.outputOffset before
		// streaming), and the server's snapshot carries the whole log.
		state = { ...state, offset: null };
		const second = 'a\nb\nc\ntail\nmore\n';
		const merged = mergeOutputEvent(state, { output: second, output_offset: 66 });
		expect(merged.output).toBe(second);
		expect(merged).toEqual({ output: second, offset: 66, action: 'replace' });
	});
});
