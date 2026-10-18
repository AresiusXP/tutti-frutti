<script lang="ts">
	import { onMount } from 'svelte';
	import { m } from '$lib/paraglide/messages';

	const UPDATE_CHECK_MS = 60 * 60 * 1000;

	let needRefresh = $state(false);
	let updateServiceWorker: ((reloadPage?: boolean) => Promise<void>) | undefined;

	onMount(() => {
		let timer: ReturnType<typeof setInterval> | undefined;
		// `virtual:pwa-register` touches `window`: import it lazily on the client only.
		void import('virtual:pwa-register').then(({ registerSW }) => {
			updateServiceWorker = registerSW({
				immediate: true,
				onNeedRefresh() {
					needRefresh = true;
				},
				onRegisteredSW(_url, registration) {
					if (registration) timer = setInterval(() => void registration.update(), UPDATE_CHECK_MS);
				}
			});
		});
		return () => clearInterval(timer);
	});
</script>

{#if needRefresh}
	<div
		role="status"
		aria-live="polite"
		class="mx-3 mt-[max(0.75rem,env(safe-area-inset-top))] flex animate-bounce-in items-center gap-3 rounded-2xl bg-banana p-2 pl-4 text-night shadow-pop"
	>
		<p class="flex-1 font-extrabold">{m.update_available()}</p>
		<button
			type="button"
			onclick={() => void updateServiceWorker?.(true)}
			class="min-h-12 rounded-xl bg-night px-5 font-extrabold text-banana active:scale-95"
		>
			{m.reload()}
		</button>
	</div>
{/if}
