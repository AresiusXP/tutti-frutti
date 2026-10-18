<script lang="ts">
	import { onMount } from 'svelte';
	import { m } from '$lib/paraglide/messages';

	/**
	 * "Delete my account" confirmation. Mounted only while open: it takes focus (on Cancel, the
	 * safe choice), keeps Tab inside and closes with Escape. The caller makes the page behind it
	 * `inert` and gives focus back to the button that opened it.
	 */
	let {
		busy = false,
		error = null,
		onconfirm,
		oncancel
	}: {
		busy?: boolean;
		/** A translated failure to show inside the dialog (it stays open for a retry). */
		error?: string | null;
		onconfirm: () => void;
		oncancel: () => void;
	} = $props();

	let confirmButton: HTMLButtonElement;
	let cancelButton: HTMLButtonElement;

	onMount(() => cancelButton.focus());

	function onkeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			event.preventDefault();
			if (!busy) oncancel();
		} else if (event.key === 'Tab') {
			const order = [confirmButton, cancelButton];
			const at = order.indexOf(document.activeElement as HTMLButtonElement);
			const next = event.shiftKey ? (at <= 0 ? order.length - 1 : at - 1) : (at + 1) % order.length;
			event.preventDefault();
			order[next].focus();
		}
	}

	// aria-disabled (not `disabled`) while busy, so focus never falls out of the dialog.
	const guard = (action: () => void) => () => {
		if (!busy) action();
	};
</script>

<svelte:window {onkeydown} />

<div
	class="fixed inset-0 z-40 flex items-end justify-center overflow-y-auto bg-night/85 p-3 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur-sm"
>
	<div
		role="dialog"
		aria-modal="true"
		aria-labelledby="delete-title"
		aria-describedby="delete-text"
		class="flex w-full max-w-md flex-col gap-3 rounded-3xl bg-night-2 p-4 ring-1 ring-night-3"
	>
		<h2 id="delete-title" class="text-2xl leading-tight font-black">{m.stats_delete_title()}</h2>
		<p id="delete-text" class="text-lg text-cream-muted">{m.stats_delete_text()}</p>
		{#if error}
			<p role="alert" class="rounded-xl bg-night px-3 py-2 font-bold text-strawberry">{error}</p>
		{/if}
		<div class="flex flex-col gap-3 pt-1">
			<button
				bind:this={confirmButton}
				type="button"
				aria-disabled={busy}
				aria-busy={busy}
				onclick={guard(onconfirm)}
				class="h-12 rounded-2xl bg-strawberry px-4 text-base font-black text-night shadow-pop transition active:translate-y-1 active:shadow-none aria-disabled:opacity-70"
			>
				{busy ? m.stats_delete_pending() : m.stats_delete_confirm()}
			</button>
			<button
				bind:this={cancelButton}
				type="button"
				aria-disabled={busy}
				onclick={guard(oncancel)}
				class="h-14 rounded-2xl bg-banana px-6 text-xl font-black text-night shadow-pop transition active:translate-y-1 active:shadow-none aria-disabled:opacity-70"
			>
				{m.stats_delete_cancel()}
			</button>
		</div>
	</div>
</div>
