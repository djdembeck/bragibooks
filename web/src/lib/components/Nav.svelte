<script lang="ts">
	import { page } from '$app/stores';
	import { Logo } from '$lib/components';
	import { Library, Import, Search, BookOpen, Settings, Loader2 } from '@lucide/svelte';

	const links = [
		{ href: '/', label: 'Dashboard', icon: Library },
		{ href: '/import', label: 'Import', icon: Import },
		{ href: '/match', label: 'Match', icon: Search },
		{ href: '/books', label: 'Books', icon: BookOpen },
		{ href: '/process', label: 'Processing', icon: Loader2 },
		{ href: '/settings', label: 'Settings', icon: Settings },
	];

	function isActive(href: string, pathname: string) {
		if (href === '/') return pathname === '/';
		return pathname === href || pathname.startsWith(`${href}/`);
	}
</script>

<header class="sticky top-0 z-30 border-b border-[var(--border-subtle)] bg-[var(--surface)]/95 backdrop-blur">
	<div class="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
		<a href="/" class="flex items-center gap-3 text-lg font-semibold text-[var(--text)] no-underline hover:opacity-90">
			<span class="flex h-11 w-11 items-center justify-center rounded-xl bg-[var(--accent)] text-[var(--accent-text)] shadow-sm">
				<Logo class="h-8 w-8" />
			</span>
			<span class="hidden sm:inline">Bragi Books</span>
		</a>

		<nav aria-label="Primary">
			<ul class="flex flex-wrap items-center gap-1">
				{#each links as link}
					{@const active = isActive(link.href, $page.url.pathname)}
					<li>
						<a
							href={link.href}
							class="group flex items-center gap-2 rounded-lg px-3 py-2 text-sm font-medium transition-all"
							class:bg-[var(--accent)]={active}
							class:text-[var(--accent-text)]={active}
							class:shadow-sm={active}
							class:text-[var(--text-secondary)]={!active}
							class:hover:bg-[var(--accent-wash)]={!active}
							class:hover:text-[var(--text)]={!active}
							aria-current={active ? 'page' : undefined}
						>
							<link.icon class="h-4 w-4" aria-hidden="true" />
							<span class="hidden sm:inline">{link.label}</span>
						</a>
					</li>
				{/each}
			</ul>
		</nav>
	</div>
</header>
