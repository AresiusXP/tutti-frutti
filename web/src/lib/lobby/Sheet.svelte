<script lang="ts">
	import { onMount, untrack, type Snippet } from 'svelte';
	import { m } from '$lib/paraglide/messages';
	import { claimBack, type BackClaim } from './sheetBack';
	import { enterStack, isTopSheet, leaveStack } from './sheetStack';

	/**
	 * Modal bottom sheet: `role="dialog"` + `aria-modal`, focus trapped inside,
	 * Escape / backdrop / close button dismiss it, focus returns to whatever
	 * opened it, and the page behind does not scroll. `role="alertdialog"` is for
	 * short confirmations: `initialFocus` (a selector inside the panel) picks the
	 * safe button to start on and `describedby` points at the text it explains.
	 * The browser / Android Back button closes it too (one history entry while it is open,
	 * see `sheetBack.ts`); it never leaves the room.
	 */
	interface Props {
		title: string;
		onclose: () => void;
		children: Snippet;
		footer?: Snippet;
		/** `full` fills the screen (the category picker); `sheet` hugs its content. */
		size?: 'sheet' | 'full';
		role?: 'dialog' | 'alertdialog';
		/** Id of the element that describes the dialog (alert dialogs). */
		describedby?: string;
		/** CSS selector, inside the panel, of the element that gets the focus first. */
		initialFocus?: string;
	}
	let {
		title,
		onclose,
		children,
		footer,
		size = 'sheet',
		role = 'dialog',
		describedby,
		initialFocus
	}: Props = $props();

	const titleId = `sheet-title-${Math.random().toString(36).slice(2, 8)}`;
	let panel = $state<HTMLElement>();
	let back = $state.raw<BackClaim | null>(null);
	let stackToken: symbol | undefined;

	const FOCUSABLE =
		'button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

	function focusables(): HTMLElement[] {
		return panel ? [...panel.querySelectorAll<HTMLElement>(FOCUSABLE)] : [];
	}

	function onkeydown(event: KeyboardEvent) {
		// a sheet opened on top of this one owns the keyboard (see sheetStack.ts)
		if (!isTopSheet(stackToken)) return;
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

	// Back (or any navigation) removed our history entry: close, as the close button would.
	$effect(() => {
		if (back?.gone) untrack(onclose);
	});

	onMount(() => {
		stackToken = enterStack();
		back = claimBack();
		const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		const overflow = document.body.style.overflow;
		document.body.style.overflow = 'hidden';
		(
			(initialFocus ? panel?.querySelector<HTMLElement>(initialFocus) : null) ??
			focusables()[0] ??
			panel
		)?.focus();
		return () => {
			if (stackToken) leaveStack(stackToken);
			back?.release();
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
		{role}
		aria-modal="true"
		aria-labelledby={titleId}
		aria-describedby={describedby}
		tabindex="-1"
		class="flex w-full max-w-md animate-bounce-in flex-col rounded-t-3xl bg-night-2 shadow-pop ring-1 ring-night-3 outline-none {size ===
		'full'
			? 'h-[94dvh]'
			: 'max-h-[92dvh]'}"
	>
		<div class="flex items-center justify-between gap-2 pt-2 pr-2 pl-5">
			<h2 id={titleId} class="min-w-0 text-xl font-black">{title}</h2>
			<button
				type="button"
				aria-label={m.lobby_close()}
				onclick={onclose}
				class="flex size-12 shrink-0 items-center justify-center rounded-xl text-xl text-cream-muted hover:text-cream"
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
