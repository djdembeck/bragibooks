import { flushSync, mount, unmount } from 'svelte';
import { describe, expect, it } from 'vitest';
import type { OutputProfile } from '#lib/types.js';
import ProfileSelectorProbe from './ProfileSelectorProbe.svelte';

function makeProfile(overrides: Partial<OutputProfile> & { id: number }): OutputProfile {
	return {
		name: `Profile ${overrides.id}`,
		output_dir: '',
		output_scheme: '',
		created_at: '2026-01-01T00:00:00Z',
		updated_at: '2026-01-01T00:00:00Z',
		...overrides
	};
}

interface ProbeHandle {
	boundText: () => string;
	summaryText: () => string | null;
	refreshProfiles: (next: OutputProfile[], error?: string | null) => void;
	teardown: () => void;
}

/**
 * Mount the probe and settle rendering. The probe echoes its bound
 * selectedProfileId — the parent-side value two-way binding writes — which is
 * what the reconciliation effect mutates. The select's own value is driven by
 * the null-safe derived and stays valid-looking even with a stale id, so it
 * must not be asserted.
 */
function mountProbe(props: {
	initialId: number | null;
	profiles: OutputProfile[];
	profilesError?: string | null;
}): ProbeHandle {
	const target = document.createElement('div');
	document.body.appendChild(target);
	const component = mount(ProfileSelectorProbe, { target, props });
	flushSync();
	return {
		boundText: () => target.querySelector('#probe-bound')?.textContent?.trim() ?? '',
		summaryText: () => target.querySelector('#probe-summary')?.textContent?.trim() ?? null,
		refreshProfiles: (next, error = null) => {
			component.refreshProfiles(next, error);
			flushSync();
		},
		teardown: () => {
			unmount(component);
			target.remove();
		}
	};
}

describe('OutputProfileSelector bound-id reconciliation', () => {
	it('keeps a bound id present in the loaded profiles', () => {
		const probe = mountProbe({
			initialId: 7,
			profiles: [makeProfile({ id: 7 }), makeProfile({ id: 3 })],
			profilesError: null
		});
		// The status message is the pre-fix behavior: the effect must not
		// clear an id the list actually backs.
		expect(probe.boundText()).toBe('7');
		probe.teardown();
	});

	it('clears a bound id that a concurrent refresh no longer lists', () => {
		const probe = mountProbe({
			initialId: 7,
			profiles: [makeProfile({ id: 7 })],
			profilesError: null
		});
		expect(probe.boundText()).toBe('7');
		// The id survives a refresh that still lists it.
		probe.refreshProfiles([makeProfile({ id: 3 }), makeProfile({ id: 7 })]);
		expect(probe.boundText()).toBe('7');
		// A stale id (404 from a concurrent deletion) is dropped.
		probe.refreshProfiles([makeProfile({ id: 3 })]);
		expect(probe.boundText()).toBe('null');
	});

	it('keeps a bound id while the profile list itself is unavailable', () => {
		const probe = mountProbe({
			initialId: 7,
			profiles: [],
			profilesError: 'Could not load output profiles.'
		});
		// The warning row covers the failed fetch; the selection cannot be
		// verified against and must not be dropped.
		expect(probe.boundText()).toBe('7');
		expect(probe.summaryText()).toBe('Could not load output profiles.');
		probe.teardown();
	});

	it('stays null without a spurious write', () => {
		const probe = mountProbe({ initialId: null, profiles: [], profilesError: null });
		expect(probe.boundText()).toBe('null');
		probe.teardown();
	});
});
