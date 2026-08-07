<script lang="ts">
	import { page } from '$app/stores';
	import { Logo } from '$lib/components';
	import { LayoutDashboard, FolderOpen, Search, Loader2, BookOpen, Settings } from '@lucide/svelte';

	const stations = [
		{ href: '/', label: 'Dashboard', icon: LayoutDashboard, number: 0 },
		{ href: '/import', label: 'Import', icon: FolderOpen, number: 1 },
		{ href: '/match', label: 'Match', icon: Search, number: 2 },
		{ href: '/process', label: 'Queue', icon: Loader2, number: 3 },
		{ href: '/books', label: 'Books', icon: BookOpen, number: 4 },
		{ href: '/settings', label: 'Settings', icon: Settings, number: 5 },
	];

	const workflowStations = [
		{ href: '/import', label: 'Intake' },
		{ href: '/match', label: 'Match' },
		{ href: '/process', label: 'Queue' },
		{ href: '/books', label: 'Finish' },
	];

	function isActive(href: string, pathname: string) {
		if (href === '/') return pathname === '/';
		return pathname === href || pathname.startsWith(`${href}/`);
	}

	function isWorkflowActive(href: string, pathname: string) {
		return pathname === href || pathname.startsWith(`${href}/`);
	}
</script>

<header class="sticky top-0 z-30 border-b border-[var(--border-subtle)] bg-[var(--bg)]">
	<div class="mx-auto flex max-w-6xl items-center gap-4 px-4 py-2.5">
		<a href="/" aria-label="Bragi Books" class="flex shrink-0 items-center gap-2.5 text-base font-bold text-[var(--enamel)] no-underline hover:opacity-80">
			<span class="flex h-8 w-8 items-center justify-center text-[var(--accent)]">
				<Logo class="h-7 w-8" />
			</span>
			<span class="hidden sm:inline">Bragi Books</span>
		</a>

		<nav aria-label="Primary" class="min-w-0 flex-1 max-w-full">
			<ul class="flex items-center gap-0.5 min-w-0 max-w-full overflow-x-auto overflow-y-hidden overscroll-x-contain sm:flex-wrap sm:overflow-visible">
				{#each stations as link}
					{@const active = isActive(link.href, $page.url.pathname)}
					<li>
						<a
							href={link.href}
							class="nav-link shrink-0"
							class:nav-link-active={active}
							aria-current={active ? 'page' : undefined}
						>
							<link.icon class="h-4 w-4 shrink-0" aria-hidden="true" />
							<span>{link.label}</span>
						</a>
					</li>
				{/each}
			</ul>
		</nav>
	</div>

	<!-- Workflow interlocking rail -->
	<nav class="rail" aria-label="Workflow">
		<div class="mx-auto flex items-center max-w-6xl px-4 py-2">
			{#each workflowStations as station, i}
				{@const active = isWorkflowActive(station.href, $page.url.pathname)}
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
