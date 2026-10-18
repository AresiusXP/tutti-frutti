<script lang="ts">
	import { m } from '$lib/paraglide/messages';

	interface Props {
		/** Seconds left to vote. */
		seconds: number;
		yes: number;
		/** Connected participants that can still vote (0 when everybody else is offline). */
		total: number;
	}
	let { seconds, yes, total }: Props = $props();
</script>

<!-- The asker's banner: above the button, in the footer stack, so it never covers a field. -->
<div
	data-testid="stop-wait-banner"
	class="flex items-center gap-3 rounded-2xl bg-night-2 px-3 py-2 ring-2 ring-banana"
>
	<span class="text-3xl" aria-hidden="true">✋</span>
	<div class="min-w-0 flex-1">
		<p class="text-base leading-tight font-black break-words">{m.play_ask_waiting()}</p>
		{#if total > 0}
			<p class="text-sm leading-tight font-bold text-cream-muted tabular-nums">
				{m.play_ask_tally({ yes, total })}
			</p>
		{/if}
	</div>
	<!-- role=timer is aria-live=off: the countdown is never read out every second. -->
	<div
		role="timer"
		aria-label={m.play_vote_time_label()}
		class="grid size-12 shrink-0 place-items-center rounded-full bg-night text-2xl font-black text-banana tabular-nums"
	>
		{seconds}
	</div>
</div>
