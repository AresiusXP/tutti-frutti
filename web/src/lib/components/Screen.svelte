<script lang="ts">
	import type { Snippet } from 'svelte';

	/**
	 * Page frame of every room screen: a centred phone-width column that fills
	 * the viewport, respects safe areas and anchors `footer` (the primary
	 * action) to the thumb zone at the bottom.
	 */
	interface Props {
		header?: Snippet;
		footer?: Snippet;
		children: Snippet;
		/** Accessible name of the screen (read by screen readers when it appears). */
		label?: string;
	}
	let { header, footer, children, label }: Props = $props();

	// The footer grows when a banner or a vote prompt joins it: fields scroll clear of its real height.
	let footerEl = $state<HTMLElement>();
	let footerHeight = $state(0);
	$effect(() => {
		const el = footerEl;
		if (!el || typeof ResizeObserver === 'undefined') return;
		const measure = () => (footerHeight = el.offsetHeight);
		measure();
		const observer = new ResizeObserver(measure);
		observer.observe(el);
		return () => observer.disconnect();
	});
</script>

<main
	aria-label={label}
	style:--footer-h={footer ? `${footerHeight}px` : undefined}
	class="mx-auto flex min-h-dvh w-full max-w-md flex-col gap-3 px-3 pt-[max(0.5rem,env(safe-area-inset-top))] pb-[max(0.75rem,env(safe-area-inset-bottom))]"
>
	{#if header}
		<header class="flex flex-col gap-2">{@render header()}</header>
	{/if}
	<div class="flex flex-1 flex-col gap-3">{@render children()}</div>
	{#if footer}
		<footer
			bind:this={footerEl}
			class="sticky bottom-0 -mx-3 flex flex-col gap-2 bg-night/90 px-3 pt-2 pb-[max(0.75rem,env(safe-area-inset-bottom))] backdrop-blur"
		>
			{@render footer()}
		</footer>
	{/if}
</main>
