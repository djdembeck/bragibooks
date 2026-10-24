<script lang="ts">
	import { page } from '$app/stores';
	import { Logo } from '$lib/components';

	// One familiar topnav for every route. No polling, no count badges:
	// batch counts live in the workspace that owns the data.
	const links = [
		{ href: '/', label: 'Overview' },
		{ href: '/import', label: 'Import' },
		{ href: '/match', label: 'Match' },
		{ href: '/process', label: 'Queue' },
		{ href: '/books', label: 'Books' },
	];

	function isActive(href: string, pathname: string) {
		if (href === '/') return pathname === '/';
		return pathname === href || pathname.startsWith(`${href}/`);
	}

	const pathname = $derived($page.url.pathname);
</script>

<!-- Skip link — first focusable element -->
<a href="#main-content" class="skip-link">Skip to main content</a>

<header class="workspace-header">
	<div class="workspace-header__inner">
		<a href="/" class="workspace-brand" aria-label="Bragi Books">
			<span class="workspace-brand__mark">
				<Logo />
			</span>
			<span class="workspace-brand__name">Bragi Books</span>
		</a>

		<nav class="workspace-nav" aria-label="Main">
			{#each links as link (link.href)}
				{@const active = isActive(link.href, pathname)}
				<a
					href={link.href}
					class="workspace-nav__link"
					aria-current={active ? 'page' : undefined}
				>
					{link.label}
				</a>
			{/each}
		</nav>

		<a
			href="/settings"
			class="workspace-settings"
			aria-current={isActive('/settings', pathname) ? 'page' : undefined}
		>
			Settings
		</a>
	</div>
</header>
