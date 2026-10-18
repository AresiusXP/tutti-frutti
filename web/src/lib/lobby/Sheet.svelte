<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	import { m } from '$lib/paraglide/messages';

	/**
	 * Modal bottom sheet: `role="dialog"` + `aria-modal`, focus trapped inside,
	 * Escape / backdrop / close button dismiss it, focus returns to whatever
	 * opened it, and the page behind does not scroll.
	 */
	interface Props {
		title: string;
		onclose: () => void;
		children: Snippet;
		footer?: Snippet;
		/** `full` fills the screen (the category picker); `sheet` hugs its content. */
		size?: 'sheet' | 'full';
	}
	let { title, onclose, children, footer, size = 'sheet' }: Props = $props();

	const titleId = `sheet-title-${Math.random().toString(36).slice(2, 8)}`;
	let panel = $state<HTMLElement>();

	const FOCUSABLE =
		'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

	function focusables(): HTMLElement[] {
		return panel ? [...panel.querySelectorAll<HTMLElement>(FOCUSABLE)] : [];
	}

	function onkeydown(event: KeyboardEvent) {
		if (event.key === 'Escape') {
			event.preventDefault();
			onclose();
			return;
		}
		if (event.key !== 'Tab') return;
		const items = focusables();
		if (items.length === 0) {
			event.preventDefault();
			panel?.focus();
			return;
		}
		const first = items[0];
		const last = items[items.length - 1];
		const active = document.activeElement;
		if (event.shiftKey && (active === first || active === panel)) {
			event.preventDefault();
			last.focus();
		} else if (!event.shiftKey && active === last) {
			event.preventDefault();
			first.focus();
		} else if (!panel?.contains(active)) {
			event.preventDefault();
			first.focus();
		}
	}

	onMount(() => {
		const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		const overflow = document.body.style.overflow;
		document.body.style.overflow = 'hidden';
		(focusables()[0] ?? panel)?.focus();
		return () => {
			document.body.style.overflow = overflow;
			if (opener?.isConnected) opener.focus();
		};
	});
</script>

<svelte:window {onkeydown} />

<div
	class="fixed inset-0 z-40 flex items-end justify-center bg-black/70"
	role="presentation"
	onclick={(event) => {
		if (event.target === event.currentTarget) onclose();
	}}
>
	<div
		bind:this={panel}
		role="dialog"
		aria-modal="true"
		aria-labelledby={titleId}
		tabindex="-1"
		class="flex w-full max-w-md animate-bounce-in flex-col rounded-t-3xl bg-night-2 shadow-pop ring-1 ring-night-3 outline-none {size ===
		'full'
			? 'h-[94dvh]'
			: 'max-h-[92dvh]'}"
	>
		<div class="flex items-center justify-between gap-2 pt-2 pr-2 pl-5">
			<h2 id={titleId} class="text-xl font-black">{title}</h2>
			<button
				type="button"
				aria-label={m.lobby_close()}
				onclick={onclose}
				class="flex size-12 items-center justify-center rounded-xl text-xl text-cream-muted hover:text-cream"
			>
				<span aria-hidden="true">✕</span>
			</button>
		</div>
		<div class="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 py-2">
			{@render children()}
		</div>
		{#if footer}
			<div class="px-4 pt-2 pb-[max(0.75rem,env(safe-area-inset-bottom))]">
				{@render footer()}
			</div>
		{:else}
			<div class="pb-[max(0.5rem,env(safe-area-inset-bottom))]"></div>
		{/if}
	</div>
</div>
