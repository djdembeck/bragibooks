<script lang="ts">
	import { page } from '$app/stores';
	import { Logo } from '$lib/components';
	import { LayoutDashboard, Settings, FolderOpen, Search, Loader2, BookOpen } from '@lucide/svelte';

	// Utility navigation — clearly separated from the workflow
	const utilityLinks = [
		{ href: '/', label: 'Dashboard', icon: LayoutDashboard },
		{ href: '/settings', label: 'Settings', icon: Settings },
	];

	// Four-stop workflow rail — the canonical task map
	const workflowStations = [
		{ href: '/import', label: 'Intake', icon: FolderOpen },
		{ href: '/match', label: 'Match', icon: Search },
		{ href: '/process', label: 'Queue', icon: Loader2 },
		{ href: '/books', label: 'Finish', icon: BookOpen },
	];

	function isActive(href: string, pathname: string) {
		if (href === '/') return pathname === '/';
		return pathname === href || pathname.startsWith(`${href}/`);
	}
</script>

<!-- Skip link — first focusable element -->
<a href="#main-content" class="skip-link">Skip to main content</a>

<header class="sticky top-0 z-30 border-b border-[var(--border-subtle)] bg-[var(--bg)]">
	<!-- Top bar: logo + utility nav -->
	<div class="mx-auto flex max-w-6xl items-center gap-4 px-4 py-2">
		<a href="/" aria-label="Bragi Books" class="flex shrink-0 items-center gap-2.5 text-base font-bold text-[var(--enamel)] no-underline hover:opacity-80 min-h-[44px] min-w-[44px]">
			<span class="flex h-8 w-8 items-center justify-center text-[var(--accent)]">
				<Logo class="h-7 w-8" />
			</span>
			<span>Bragi Books</span>
		</a>

		<nav aria-label="Utility" class="ml-auto flex items-center gap-0.5">
			{#each utilityLinks as link}
				{@const active = isActive(link.href, $page.url.pathname)}
				<a
					href={link.href}
					class="utility-link"
					class:utility-link-active={active}
					aria-label={link.label}
					aria-current={active ? 'page' : undefined}
				>
					<link.icon class="h-4 w-4 shrink-0" aria-hidden="true" />
					<span class="utility-link__label">{link.label}</span>
				</a>
			{/each}
		</nav>
	</div>

	<!-- Workflow interlocking rail — visually primary -->
	<nav class="rail" aria-label="Workflow">
		<div class="mx-auto flex items-center max-w-6xl px-4 py-2">
			{#each workflowStations as station, i}
				{@const active = isActive(station.href, $page.url.pathname)}
				{#if i > 0}
					<div class="rail-segment" aria-hidden="true"></div>
				{/if}
				<a
					href={station.href}
					class="rail-station"
					class:rail-station-active={active}
					aria-current={active ? 'step' : undefined}
				>
					<div class="rail-station-dot" aria-hidden="true"></div>
					<span class="rail-station-label">{station.label}</span>
				</a>
			{/each}
		</div>
	</nav>
</header>