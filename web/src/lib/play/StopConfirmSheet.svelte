<script lang="ts">
	import Sheet from '$lib/lobby/Sheet.svelte';
	import { m } from '$lib/paraglide/messages';

	interface Props {
		letter: string;
		/** Categories still empty. */
		empty: number;
		/** Answers that do not start with the letter. */
		wrong: number;
		/** How long the others have to agree. */
		seconds: number;
		onconfirm: () => void;
		oncancel: () => void;
	}
	let { letter, empty, wrong, seconds, onconfirm, oncancel }: Props = $props();

	const button =
		'min-h-14 w-full touch-manipulation rounded-2xl px-4 py-2 text-lg leading-tight font-black shadow-pop transition active:translate-y-1 active:shadow-none';
</script>

<!-- Safe default first: focus starts on "keep playing", Escape and the backdrop mean the same. -->
<Sheet
	title={m.play_ask_title()}
	role="alertdialog"
	describedby="stop-ask-desc"
	initialFocus="[data-keep]"
	onclose={oncancel}
>
	<div id="stop-ask-desc" class="flex flex-col gap-2 pb-1">
		{#if empty > 0}
			<p class="text-lg leading-snug font-extrabold">
				{empty === 1 ? m.play_ask_missing_one() : m.play_ask_missing_many({ count: empty })}
			</p>
		{/if}
		{#if wrong > 0}
			<p class="text-lg leading-snug font-extrabold text-strawberry">
				{wrong === 1
					? m.play_ask_wrong_one({ letter })
					: m.play_ask_wrong_many({ count: wrong, letter })}
			</p>
		{/if}
		<p class="text-base text-cream-muted">{m.play_ask_rule({ seconds })}</p>
	</div>
	{#snippet footer()}
		<div class="flex flex-col gap-2">
			<button
				type="button"
				data-testid="stop-ask-confirm"
				onclick={onconfirm}
				class="{button} bg-strawberry text-night"
			>
				{m.play_ask_confirm()}
			</button>
			<button
				type="button"
				data-keep
				data-testid="stop-ask-keep"
				onclick={oncancel}
				class="{button} bg-night-3 text-cream"
			>
				{m.play_ask_keep()}
			</button>
		</div>
	{/snippet}
</Sheet>
