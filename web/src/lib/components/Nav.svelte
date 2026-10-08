<script lang="ts">
	import { page } from '$app/stores';
	import { Logo, StationRail } from '$lib/components';
	import { Gauge, Settings } from '@lucide/svelte';

	// Utility navigation — clearly separated from the workflow
	const utilityLinks = [
		{ href: '/', label: 'Dashboard', icon: Gauge },
		{ href: '/settings', label: 'Settings', icon: Settings },
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
	<div class="mx-auto flex max-w-6xl items-center gap-3 px-4 py-2 sm:gap-4">
		<a href="/" aria-label="Bragi Books" class="header-brand flex shrink-0 items-center gap-2.5 text-base font-bold text-[var(--enamel)] no-underline hover:opacity-80 min-h-[44px] min-w-[44px]">
			<span class="flex h-8 w-8 items-center justify-center text-[var(--accent)]">
				<Logo class="h-8 w-8" />
			</span>
			<span class="header-brand-name">Bragi Books</span>
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

	<!-- Slim workflow strip — hidden on the dashboard, which owns the bays -->
	{#if $page.url.pathname !== '/'}
		<StationRail />
	{/if}
</header>
