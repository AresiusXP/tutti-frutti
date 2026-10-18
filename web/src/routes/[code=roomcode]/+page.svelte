<script lang="ts">
	import { afterNavigate, replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { BRAND } from '$lib/brand';
	import RoomRoute from './RoomRoute.svelte';

	// Invite links work in any case (/kt7p): the canonical code is upper-case.
	const requested = $derived(page.params.code ?? '');
	const code = $derived(requested.toUpperCase());

	// The router is only ready after the first navigation: canonicalise the URL from there on.
	afterNavigate(() => {
		const path = resolve('/[code=roomcode]', { code });
		if (page.url.pathname === path) return;
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- resolved path plus the original query/hash
		replaceState(path + page.url.search + page.url.hash, page.state);
	});
</script>

<svelte:head>
	<title>{BRAND} · {code}</title>
</svelte:head>

{#key code}
	<RoomRoute {code} />
{/key}
