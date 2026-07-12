<script lang="ts">
	import { page } from '$app/stores';
	import { Logo } from '$lib/components';

	const links = [
		{ href: '/', label: 'Dashboard' },
		{ href: '/import', label: 'Import' },
		{ href: '/match', label: 'Match' },
		{ href: '/books', label: 'Books' },
		{ href: '/process', label: 'Processing' },
		{ href: '/settings', label: 'Settings' },
	];

	function isActive(href: string, pathname: string) {
		if (href === '/') return pathname === '/';
		return pathname === href || pathname.startsWith(`${href}/`);
	}
</script>

<header class="sticky top-0 z-30 border-b border-[var(--border-subtle)] bg-[var(--surface)]/95 backdrop-blur">
	<div class="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
		<a href="/" class="flex items-center gap-2 text-lg font-semibold text-[var(--text)] no-underline hover:opacity-90">
			<span class="flex h-8 w-8 items-center justify-center rounded-md bg-[var(--accent)] text-[var(--text)]">
				<Logo class="h-6 w-6" />
			</span>
			Bragi Books
		</a>

		<nav aria-label="Primary">
			<ul class="flex flex-wrap items-center gap-1 sm:gap-2">
				{#each links as link}
					{@const active = isActive(link.href, $page.url.pathname)}
					<li>
						<a
							href={link.href}
							class="rounded-md px-2.5 py-1.5 text-sm font-medium transition-colors sm:px-3"
							class:text-[var(--accent)]={active}
							class:bg-[var(--surface-hover)]={active}
							class:text-[var(--text-secondary)]={!active}
							class:hover:text-[var(--text)]={!active}
							aria-current={active ? 'page' : undefined}
						>
							{link.label}
						</a>
					</li>
				{/each}
			</ul>
		</nav>
	</div>
</header>
