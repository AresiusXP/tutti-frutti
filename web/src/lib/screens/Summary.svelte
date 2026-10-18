<script lang="ts">
	import { SvelteSet } from 'svelte/reactivity';
	import Button from '$lib/components/Button.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import type { RoomSession } from '$lib/game/session';
	import { m } from '$lib/paraglide/messages';
	import ConfirmBar from '$lib/review/ConfirmBar.svelte';
	import RankRow from '$lib/review/RankRow.svelte';
	import { buildRanking } from '$lib/review/ranking';
	import { lastRoundReason } from '$lib/review/rounds';

	/**
	 * Round summary: players ranked by running total with this round's points
	 * (+N), rank arrows and an expandable per-category breakdown. The host
	 * continues; on the last round "end game" is the primary action
	 * (`next_round` would also lead to the final: see `lastRoundReason`).
	 */
	let { session }: { session: RoomSession } = $props();

	const view = $derived(session.view);
	const round = $derived(view?.round);
	const result = $derived(round?.result);
	const rows = $derived(
		view ? buildRanking(view.players, result, { compare: view.history.length > 1 }) : []
	);
	const last = $derived(
		view ? lastRoundReason(view.settings, view.roundNum, view.usedLetters) : null
	);
	const labels = $derived(view?.categoryLabels ?? []);

	const open = new SvelteSet<string>();
	let confirmEnd = $state(false);

	function toggle(id: string) {
		if (open.has(id)) open.delete(id);
		else open.add(id);
	}
</script>

{#if view && round}
	<Screen label={m.summary_label({ round: round.number })}>
		{#snippet header()}
			<div class="flex flex-col gap-1 pt-2">
				<h1 class="text-3xl leading-tight font-black">
					{m.summary_title({ round: round.number, letter: round.letter })}
				</h1>
				{#if last}
					<p class="flex flex-wrap items-center gap-2" data-testid="last-round">
						<span class="rounded-full bg-strawberry px-3 py-0.5 text-sm font-black text-night">
							{m.summary_last_round()}
						</span>
						<span class="text-cream-muted">
							{last === 'rounds' ? m.summary_last_rounds() : m.summary_last_letters()}
						</span>
					</p>
				{/if}
			</div>
		{/snippet}

		<ol aria-label={m.summary_ranking()} class="flex flex-col gap-2">
			{#each rows as row (row.player.id)}
				<RankRow
					{row}
					isMe={row.player.id === view.youId}
					cells={result?.players[row.player.id]?.cells}
					{labels}
					letter={round.letter}
					showRound
					expanded={open.has(row.player.id)}
					ontoggle={() => toggle(row.player.id)}
				/>
			{/each}
		</ol>

		{#snippet footer()}
			{#if session.isHost}
				{#if confirmEnd}
					<ConfirmBar
						message={m.summary_end_confirm()}
						yes={m.summary_end_confirm_yes()}
						no={m.summary_end_confirm_no()}
						onyes={() => {
							confirmEnd = false;
							session.endGame();
						}}
						onno={() => (confirmEnd = false)}
					/>
				{:else if last}
					<Button tone="strawberry" onclick={() => session.endGame()}>
						{m.summary_end_game()}
					</Button>
				{:else}
					<Button tone="banana" onclick={() => session.nextRound()}>
						{m.summary_next_round()}
					</Button>
					<Button tone="plain" size="md" onclick={() => (confirmEnd = true)}>
						{m.summary_end_game()}
					</Button>
				{/if}
			{:else}
				<p class="py-3 text-center text-lg font-bold text-cream-muted" role="status">
					{m.summary_waiting()}
				</p>
			{/if}
		{/snippet}
	</Screen>
{/if}
