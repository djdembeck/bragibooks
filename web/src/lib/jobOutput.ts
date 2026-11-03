/**
 * The client side of the SSE output contract: fold one parsed output event
 * into the item's accumulated log and cursor. The output branches of the
 * live stream handler live here so the decision is unit-testable.
 *
 * The first offset event of a connection is the server's FULL log snapshot
 * (offset = buffer end); every later event is ONLY the bytes appended after
 * it, with its offset at the new buffer end — so a contiguous suffix always
 * carries an offset GREATER than the cursor (the equal case is an empty
 * suffix, which the server never sends). Offsets at or below the cursor are
 * replays of already-logged bytes: ignore.
 */

/** The item's accumulated log state the merge decision updates. */
export interface OutputState {
	/** Accumulated log text the item currently holds. */
	output: string;
	/** Buffer-end offset the log holds; null until the first offset event. */
	offset: number | null;
}

/** The output-carrying fields of one parsed SSE data payload. */
export interface OutputEvent {
	output?: string;
	output_offset?: number;
}

/** The merge outcome plus which action the log tracker must mirror. */
export interface MergedOutput extends OutputState {
	/**
	 * 'replace' handed a full snapshot; 'append' a new suffix; 'ignore' left
	 * the log untouched (replay/nudge/no payload).
	 */
	action: 'replace' | 'append' | 'ignore';
}

export function mergeOutputEvent(state: OutputState, parsed: OutputEvent): MergedOutput {
	if (typeof parsed.output_offset === 'number') {
		if (state.offset === null) {
			// First offset event: the initial snapshot replaces.
			return { output: parsed.output ?? '', offset: parsed.output_offset, action: 'replace' };
		}
		if (parsed.output_offset > state.offset) {
			// New suffix: the payload is the unseen bytes; advance the
			// cursor to the new buffer end.
			return {
				output: state.output + (parsed.output ?? ''),
				offset: parsed.output_offset,
				action: 'append'
			};
		}
		// Replay of already-logged bytes, or the cursor-equal nudge: the
		// log is already current.
		return { output: state.output, offset: state.offset, action: 'ignore' };
	}
	if (parsed.output) {
		// Offset-less payload: full snapshot (the no-in-memory-state DB
		// path) — replaces and resets the cursor.
		return { output: parsed.output, offset: null, action: 'replace' };
	}
	// No output field at all (heartbeat/status payload): untouched.
	return { output: state.output, offset: state.offset, action: 'ignore' };
}
