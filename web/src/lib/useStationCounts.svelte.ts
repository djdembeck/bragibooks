import { get } from '$lib/api';
import type { BooksListResponse } from '$lib/types';
import { delayedLoad } from '$lib/delayedLoad.svelte';

export type StationId = 'intake' | 'match' | 'queue' | 'finish';

export interface StationTotals {
	pending: number;
	matched: number;
	processing: number;
	done: number;
	error: number;
}

export interface Station {
	id: StationId;
	number: string;
	label: string;
	href: string;
}

/** The four stations, in workflow order. */
export const STATIONS: Station[] = [
	{ id: 'intake', number: '#01', label: 'INTAKE', href: '/import' },
	{ id: 'match', number: '#02', label: 'MATCH', href: '/match' },
	{ id: 'queue', number: '#03', label: 'QUEUE', href: '/process' },
	{ id: 'finish', number: '#04', label: 'FINISH', href: '/books' }
];

/**
 * The station the "now" light sits on. Pure — shared by the Nav
 * rail and the dashboard so they always agree.
 */
export function activeStationFor(totals: StationTotals, _currentPath: string): StationId {
	if (totals.processing > 0) return 'queue';
	if (totals.matched > 0) return 'match';
	if (totals.pending > 0) return 'intake';
	return 'finish';
}

const ZERO: StationTotals = { pending: 0, matched: 0, processing: 0, done: 0, error: 0 };

const STATUSES: Array<[keyof StationTotals, string]> = [
	['pending', 'pending'],
	['matched', 'matched'],
	['processing', 'processing'],
	['done', 'done'],
	['error', 'error']
];

export interface StationCounts {
	totals: StationTotals;
	loading: boolean;
	error: string | null;
	reload: () => void;
}

/**
 * Live workflow counts — one `GET /api/books?status=X&limit=1`
 * per status, reading `.total`. Uses the shared `delayedLoad`
 * pattern so skeletons only appear after the delay.
 */
export function useStationCounts(): StationCounts {
	let totals = $state<StationTotals>({ ...ZERO });
	const dl = delayedLoad({ delay: 200 });

	let loading = $state(false);
	let generation = 0;

	async function load() {
		const g = ++generation;
		loading = true;
		await dl.run(async () => {
			const responses = await Promise.all(STATUSES.map(([, status]) => get<BooksListResponse>(`/books?status=${status}&limit=1`)));
			if (g !== generation) return;
			const next: StationTotals = { ...ZERO };
			responses.forEach((res, i) => {
				next[STATUSES[i][0]] = res?.total ?? 0;
			});
			totals = next;
		});
		if (g === generation) loading = false;
	}

	let initialized = false;
	$effect(() => {
		if (initialized) return;
		initialized = true;
		load();
	});

	return {
		get totals() {
			return totals;
		},
		get loading() {
			return loading;
		},
		get error() {
			return dl.error;
		},
		reload: load
	};
}
