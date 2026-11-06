/**
 * The single next action the Overview page offers, chosen by urgency from the
 * library counts: failures first, then matching, then queued work, then
 * intake. Pure over plain numbers so the ladder can be tested without the
 * page's load state.
 */

export interface NextActionCounts {
	errors: number;
	pending: number;
	matched: number;
	processing: number;
	/** True when a job is running or queued; makes "View queue" the move. */
	queueRunning: boolean;
}

export interface NextAction {
	href: string;
	label: string;
}

export function nextAction(counts: NextActionCounts): NextAction {
	if (counts.errors > 0) {
		return {
			href: '/books?status=error',
			label: `Review ${counts.errors} error${counts.errors === 1 ? '' : 's'}`,
		};
	}
	if (counts.pending > 0) {
		return {
			href: '/match',
			label: `Match ${counts.pending} book${counts.pending === 1 ? '' : 's'}`,
		};
	}
	// "View queue" is passive navigation; when the move is to work a
	// ready batch, name that work instead.
	if (counts.matched > 0 && counts.processing === 0 && !counts.queueRunning) {
		return {
			href: '/process',
			label: `Review ${counts.matched} ready book${counts.matched === 1 ? '' : 's'}`,
		};
	}
	if (counts.matched > 0 || counts.processing > 0 || counts.queueRunning) {
		return { href: '/process', label: 'View queue' };
	}
	return { href: '/import', label: 'Import books' };
}

/**
 * The Overview page's "the library is empty" gate. Before the fetch settles
 * and after a failed one, the library is not empty — it is unknown.
 */
export function libraryEmpty(options: {
	hasLoaded: boolean;
	error: unknown;
	totalBooks: number;
}): boolean {
	return options.hasLoaded && !options.error && options.totalBooks === 0;
}
