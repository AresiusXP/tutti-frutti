<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { HistoryRound } from './ranking';

	/** Expandable per-round history: every round with the totals of its players, best first. */
	let { rounds, meId }: { rounds: HistoryRound[]; meId: string } = $props();
	let open = $state(false);
</script>

{#if rounds.length > 0}
	<section class="rounded-2xl border-2 border-night-3 bg-night-2">
		<h2>
			<button
				type="button"
				class="flex h-14 w-full items-center justify-between gap-2 px-3 text-left text-lg font-black"
				aria-expanded={open}
				aria-controls="final-history"
				onclick={() => (open = !open)}
			>
				{m.final_history()}
				<span aria-hidden="true" class="text-cream-muted">{open ? '▴' : '▾'}</span>
			</button>
		</h2>
		{#if open}
			<ol id="final-history" class="flex flex-col gap-3 border-t-2 border-night-3 p-3">
				{#each rounds as round (round.round)}
					<li data-round={round.round}>
						<h3 class="mb-1 text-sm font-black text-cream-muted">
							{m.final_history_round({ round: round.round, letter: round.letter })}
						</h3>
						<ul class="flex flex-wrap gap-1.5">
							{#each round.entries as entry (entry.player.id)}
								<li
									class="rounded-full px-3 py-1 text-sm tabular-nums {entry.total === round.best &&
									round.best > 0
										? 'bg-lime font-black text-night'
										: 'bg-night-3 text-cream'} {entry.player.id === meId
										? 'ring-2 ring-banana'
										: ''} {entry.player.left ? 'opacity-70' : ''}"
								>
									{entry.player.name} <span class="font-black">{entry.total}</span>
								</li>
							{/each}
						</ul>
					</li>
				{/each}
			</ol>
		{/if}
	</section>
{/if}
