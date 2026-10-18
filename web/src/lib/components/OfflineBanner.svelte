<script lang="ts">
	import { onMount } from 'svelte';
	import { m } from '$lib/paraglide/messages';

	let online = $state(true);

	onMount(() => {
		online = navigator.onLine;
	});
</script>

<svelte:window ononline={() => (online = true)} onoffline={() => (online = false)} />

{#if !online}
	<div
		role="status"
		aria-live="polite"
		class="mx-auto mt-[max(0.75rem,env(safe-area-inset-top))] flex min-h-12 animate-bounce-in items-center rounded-full bg-night-3 px-5 font-bold text-cream ring-2 ring-strawberry"
	>
		<span aria-hidden="true" class="mr-2">📡</span>
		{m.offline()}
	</div>
{/if}
