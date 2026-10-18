<script lang="ts">
	import { onMount } from 'svelte';
	import Screen from '$lib/components/Screen.svelte';
	import { m } from '$lib/paraglide/messages';

	/**
	 * First connection in progress. Nothing is drawn for the first 300 ms so a
	 * fast connect (the common case) does not flash a loading screen.
	 */
	let { code, delay = 300 }: { code: string; delay?: number } = $props();

	let visible = $state(false);
	onMount(() => {
		const timer = setTimeout(() => (visible = true), delay);
		return () => clearTimeout(timer);
	});
</script>

<Screen label={m.net_connecting()}>
	<div role="status" aria-live="polite" class="flex flex-1 flex-col items-center justify-center">
		{#if visible}
			<div class="flex w-full animate-bounce-in flex-col items-center gap-5 text-center">
				<span class="animate-bounce text-6xl" aria-hidden="true">🍓</span>
				<div class="flex flex-col gap-1">
					<p class="text-2xl font-black">{m.net_connecting()}</p>
					<p class="text-cream-muted">{m.net_connecting_hint({ code })}</p>
				</div>
				<div class="flex w-full flex-col gap-2" aria-hidden="true">
					{#each [0, 1, 2] as row (row)}
						<div class="flex h-14 animate-pulse items-center gap-3 rounded-2xl bg-night-2 px-3">
							<span class="size-9 rounded-full bg-night-3"></span>
							<span class="h-4 flex-1 rounded-full bg-night-3" style:max-width="{70 - row * 15}%"
							></span>
						</div>
					{/each}
				</div>
			</div>
		{/if}
	</div>
</Screen>
