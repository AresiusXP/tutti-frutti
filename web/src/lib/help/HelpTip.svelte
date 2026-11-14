<script lang="ts">
	import { m } from '#lib/paraglide/messages.js';
	import HelpSheet from './HelpSheet.svelte';
	import { tips, type TipId } from './tips.svelte.ts';

	/**
	 * A one-time tip card at the top of a screen: what the controls on it do, with a way to the full
	 * guide. The caller decides who gets it (`!tips.seen(id)` and the right role). It counts as seen when
	 * the player closes it AND when the screen it was on goes away, so it never comes back round after
	 * round for somebody who simply played on.
	 */
	let { id, text }: { id: TipId; text: string } = $props();

	let helpOpen = $state(false);

	$effect(() => () => tips.markSeen(id));
</script>

<section
	aria-label={m.help_tip_label()}
	data-testid="help-tip"
	class="flex flex-col gap-2 rounded-2xl bg-night-2 p-3 ring-1 ring-banana/40"
>
	<p class="flex gap-2 text-base leading-snug">
		<span aria-hidden="true">💡</span>
		<span class="min-w-0 [overflow-wrap:anywhere]">{text}</span>
	</p>
	<div class="flex gap-2">
		<button
			type="button"
			onclick={() => tips.markSeen(id)}
			class="h-12 flex-1 rounded-xl bg-banana px-3 font-black text-night transition active:translate-y-0.5"
		>
			{m.help_got_it()}
		</button>
		<button
			type="button"
			aria-haspopup="dialog"
			onclick={() => (helpOpen = true)}
			class="h-12 flex-1 rounded-xl bg-night px-3 font-bold text-cream ring-1 ring-night-3 transition active:bg-night-3"
		>
			{m.help_title()}
		</button>
	</div>
</section>

{#if helpOpen}
	<HelpSheet section={id} onclose={() => (helpOpen = false)} />
{/if}
