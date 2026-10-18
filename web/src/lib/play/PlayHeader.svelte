<script lang="ts">
	import type { Snippet } from 'svelte';
	import { m } from '$lib/paraglide/messages';
	import ProgressStrip from './ProgressStrip.svelte';
	import type { ProgressEntry } from './progress';
	import type { TimerTone } from './time';

	interface Props {
		letter: string;
		round: string;
		/** `m:ss` when the round has a time limit, otherwise null. */
		clock: string | null;
		tone: TimerTone;
		entries: ProgressEntry[];
		/** The big letter pops in (the letter was just revealed). */
		pop?: boolean;
		banner?: Snippet;
	}
	let { letter, round, clock, tone, entries, pop = false, banner }: Props = $props();

	const toneClass: Record<TimerTone, string> = {
		calm: 'bg-night-3 text-cream',
		warn: 'bg-tangerine text-night',
		danger: 'bg-strawberry text-night timer-pulse'
	};
</script>

<!-- Sticks to the top while the fields scroll; the negative margin cancels the page's top padding. -->
<div
	class="sticky top-0 z-20 -mx-3 -mt-[max(0.5rem,env(safe-area-inset-top))] bg-night/95 px-3 pt-[max(0.5rem,env(safe-area-inset-top))] pb-2 backdrop-blur"
	data-testid="play-header"
>
	<div class="flex items-start gap-3">
		<div
			role="img"
			aria-label={m.play_letter_name({ letter })}
			class="grid size-[76px] shrink-0 place-items-center rounded-2xl bg-banana text-[64px] leading-none font-black text-night shadow-pop-sm [@media(max-height:520px)]:size-12 [@media(max-height:520px)]:text-[36px] {pop
				? 'animate-bounce-in'
				: ''}"
		>
			<span aria-hidden="true">{letter}</span>
		</div>
		<div class="flex min-w-0 flex-1 flex-col gap-1.5">
			<div class="flex items-center justify-between gap-2">
				<p class="text-sm font-extrabold tracking-wide text-cream-muted uppercase">{round}</p>
				{#if clock !== null}
					<!-- role=timer is aria-live=off: the clock is never read out every second. -->
					<span
						role="timer"
						aria-label={m.play_time_left()}
						class="rounded-full px-3 py-0.5 text-xl font-black tabular-nums {toneClass[tone]}"
					>
						{clock}
					</span>
				{/if}
			</div>
			<div class="[@media(max-height:520px)]:hidden">
				<ProgressStrip {entries} />
			</div>
		</div>
	</div>
	{#if banner}
		<div class="mt-2">{@render banner()}</div>
	{/if}
</div>

<style>
	.timer-pulse {
		animation: timer-pulse 1s ease-in-out infinite;
	}
	@keyframes timer-pulse {
		50% {
			transform: scale(1.08);
		}
	}
</style>
