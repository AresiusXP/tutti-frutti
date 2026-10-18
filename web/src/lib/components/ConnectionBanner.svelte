<script lang="ts">
	import { onMount } from 'svelte';
	import type { RoomSession } from '$lib/game/session';
	import { m } from '$lib/paraglide/messages';

	/**
	 * "Reconectando…" pill over the last known screen while the socket is down
	 * (or the first connect after a drop). It never blocks touches and stays
	 * quiet when the browser is offline: the global offline banner says it better.
	 */
	let { session }: { session: RoomSession } = $props();

	let online = $state(true);
	onMount(() => {
		online = navigator.onLine;
	});

	const retrying = $derived(
		session.status === 'reconnecting' || (session.status === 'connecting' && !!session.view)
	);
</script>

<svelte:window ononline={() => (online = true)} onoffline={() => (online = false)} />

<div
	role="status"
	aria-live="polite"
	class="pointer-events-none fixed inset-x-0 top-0 z-40 flex justify-center px-3 pt-[max(0.25rem,env(safe-area-inset-top))]"
>
	{#if retrying && online}
		<div
			class="flex min-h-10 animate-bounce-in items-center gap-2 rounded-full bg-night-3 px-4 text-sm font-bold text-cream shadow-pop-sm ring-2 ring-banana"
		>
			<span
				class="size-4 animate-spin rounded-full border-[3px] border-cream-muted border-t-banana"
				aria-hidden="true"
			></span>
			{m.net_reconnecting()}
		</div>
	{/if}
</div>
