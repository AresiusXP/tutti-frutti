<script lang="ts">
	import { onMount } from 'svelte';
	import { m } from '$lib/paraglide/messages';
	import { applyUpdate, startUpdates, update } from '$lib/pwa/update.svelte';
	import { updateErrorText } from '$lib/pwa/errors';

	onMount(() => startUpdates());
</script>

{#if update.needRefresh}
	<div
		role="status"
		aria-live="polite"
		class="mx-3 mt-[max(0.75rem,env(safe-area-inset-top))] flex animate-bounce-in flex-wrap items-center gap-x-3 gap-y-1 rounded-2xl bg-banana p-2 pl-4 text-night shadow-pop"
	>
		<p class="flex-1 font-extrabold">{m.update_available()}</p>
		<button
			type="button"
			disabled={update.applying}
			onclick={() => void applyUpdate()}
			class="min-h-12 rounded-xl bg-night px-5 font-extrabold text-banana active:scale-95 disabled:opacity-60"
		>
			{m.reload()}
		</button>
		{#if update.error}
			<p class="basis-full pr-2 pb-1 text-sm font-bold">{updateErrorText(update.error)}</p>
		{/if}
	</div>
{/if}
