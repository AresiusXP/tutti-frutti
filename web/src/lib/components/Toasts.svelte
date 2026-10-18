<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { toasts } from '$lib/toast.svelte';
</script>

<div
	class="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col items-center gap-2 p-4 pb-[max(1rem,env(safe-area-inset-bottom))]"
	aria-live="polite"
>
	{#each toasts.items as toast (toast.id)}
		<div
			class="pointer-events-auto flex animate-bounce-in items-center gap-1 rounded-2xl bg-cream py-1 pr-1 pl-5 font-extrabold text-night shadow-pop"
		>
			<span>{toast.text}</span>
			<button
				type="button"
				aria-label={m.toast_dismiss()}
				onclick={() => toasts.dismiss(toast.id)}
				class="flex size-12 shrink-0 items-center justify-center rounded-xl text-xl"
			>
				<span aria-hidden="true">✕</span>
			</button>
		</div>
	{/each}
</div>
