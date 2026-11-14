<script lang="ts">
	import { m } from '#lib/paraglide/messages.js';
	import HelpSheet from './HelpSheet.svelte';
	import type { HelpSection } from './sections.ts';

	/**
	 * Opens the "How to play" guide. `icon` is a round 48 px "?" (Home, the lobby and the review header);
	 * `text` is a quiet underlined link for the foot of a screen. `section` is the part of the guide that
	 * opens first (the one that matches the screen the player is on).
	 */
	let {
		section = 'basics',
		variant = 'icon',
		class: extra = ''
	}: { section?: HelpSection; variant?: 'icon' | 'text'; class?: string } = $props();

	let open = $state(false);
</script>

<button
	type="button"
	aria-haspopup="dialog"
	aria-label={variant === 'icon' ? m.help_title() : undefined}
	data-testid="help-button"
	onclick={() => (open = true)}
	class="flex shrink-0 items-center justify-center transition {variant === 'icon'
		? 'size-12 rounded-full bg-night-2 text-cream-muted ring-1 ring-night-3 active:bg-night-3'
		: 'h-12 gap-2 rounded-xl px-4 text-sm font-bold text-cream-muted underline underline-offset-4 hover:text-cream active:bg-night-3'} {extra}"
>
	<svg
		aria-hidden="true"
		viewBox="0 0 24 24"
		class={variant === 'icon' ? 'size-6' : 'size-5 shrink-0'}
		fill="none"
		stroke="currentColor"
		stroke-width="2"
		stroke-linecap="round"
		stroke-linejoin="round"
	>
		<circle cx="12" cy="12" r="9" />
		<path d="M9.6 9.4a2.5 2.5 0 0 1 4.8.9c0 1.7-2.4 2.1-2.4 3.7M12 17h.01" />
	</svg>
	{#if variant === 'text'}{m.help_title()}{/if}
</button>

{#if open}
	<HelpSheet {section} onclose={() => (open = false)} />
{/if}
