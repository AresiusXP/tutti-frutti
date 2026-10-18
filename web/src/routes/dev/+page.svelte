<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import type { FakeSession } from '$lib/game/fake.svelte';
	import { i18n } from '$lib/i18n.svelte';
	import RoomScreen from '$lib/screens/RoomScreen.svelte';
	import type { PageProps } from './$types';

	/**
	 * Dev playground: renders any room screen with scripted data, no server.
	 *   /dev                      -> list of scenarios
	 *   /dev?s=lobby-host         -> that scenario
	 *   /dev?s=...&lang=en        -> in English (es | en | nl)
	 *   /dev?s=...&bare=1         -> without the debug panel (for screenshots)
	 * A scenario can set `session.devTap` (a data-testid) to open a sheet by itself.
	 * Only available in `vite dev`; in a production build `+page.ts` redirects home
	 * and the scenarios are not bundled.
	 */
	let { data }: PageProps = $props();
	const scenarios = $derived(data.scenarios);
	const names = $derived(Object.keys(scenarios).sort());
	const name = $derived(page.url.searchParams.get('s') ?? '');
	const bare = $derived(page.url.searchParams.get('bare') === '1');
	const lang = $derived(page.url.searchParams.get('lang'));
	const session: FakeSession | null = $derived(name && scenarios[name] ? scenarios[name]() : null);

	$effect(() => {
		if (lang) i18n.set(lang);
	});

	// Scenarios that start with something tapped (a confirmation sheet open): tap it once the screen is up.
	$effect(() => {
		const testId = session?.devTap;
		if (!testId) return;
		const timer = setTimeout(
			() => document.querySelector<HTMLElement>(`[data-testid="${testId}"]`)?.click(),
			50
		);
		return () => clearTimeout(timer);
	});
</script>

{#if session}
	<RoomScreen {session} />
	{#if !bare}
		<details
			class="fixed right-2 bottom-2 z-50 max-w-[90vw] rounded-lg bg-black/80 p-2 text-xs text-white"
		>
			<summary class="cursor-pointer">dev · {name}</summary>
			<a class="underline" href={resolve('/dev')}>all scenarios</a>
			<pre class="max-h-48 overflow-auto">{JSON.stringify(session.sent, null, 1)}</pre>
		</details>
	{/if}
{:else}
	<main class="mx-auto max-w-md p-4">
		<h1 class="mb-3 text-2xl font-black">Dev scenarios</h1>
		<ul class="flex flex-col gap-2">
			{#each names as n (n)}
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolved path + query string -->
				<li><a class="underline" href={`${resolve('/dev')}?s=${n}`}>{n}</a></li>
			{/each}
		</ul>
	</main>
{/if}
