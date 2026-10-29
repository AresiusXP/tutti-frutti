<script lang="ts">
	import Button from '$lib/components/Button.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import SupportLink from '$lib/components/SupportLink.svelte';
	import type { RoomSession } from '$lib/game/session';
	import { i18n } from '$lib/i18n.svelte';
	import { m } from '$lib/paraglide/messages';
	import Confetti from '$lib/review/Confetti.svelte';
	import { joinNames } from '$lib/review/players';
	import Podium from '$lib/review/Podium.svelte';
	import { buildPodium } from '$lib/review/podium';
	import RankRow from '$lib/review/RankRow.svelte';
	import { buildHistory } from '$lib/review/ranking';
	import RoundHistory from '$lib/review/RoundHistory.svelte';

	/**
	 * Final: podium of the winners (ties share a step), the full ranking and an
	 * expandable per-round history. The host starts a rematch; everybody can go
	 * home (`leave()`: the room shell navigates when the session closes).
	 */
	let { session }: { session: RoomSession } = $props();

	const view = $derived(session.view);
	const podium = $derived(view ? buildPodium(view) : null);
	const history = $derived(view ? buildHistory(view.history, view.players) : []);
	const winnerNames = $derived(
		(podium?.winnerIds ?? []).map((id) => view?.players.find((p) => p.id === id)?.name ?? id)
	);
	const headline = $derived(
		winnerNames.length === 0
			? m.final_title()
			: winnerNames.length === 1
				? m.final_winner({ name: winnerNames[0] })
				: m.final_tie({ names: joinNames(winnerNames, i18n.locale) })
	);
</script>

{#if view && podium}
	<Screen label={m.final_label()}>
		{#snippet header()}
			<div class="flex flex-col items-center gap-1 pt-3 text-center">
				<h1 class="text-lg font-black tracking-wide text-cream-muted uppercase">
					{m.final_title()}
				</h1>
				<p class="text-3xl leading-tight font-black [overflow-wrap:anywhere]" aria-live="polite">
					{headline}
				</p>
			</div>
		{/snippet}

		<Confetti />
		{#if session.visibility === 'public'}
			<p class="text-center text-sm text-cream-muted">{m.rooms_casual()}</p>
		{/if}

		{#if podium.places.length > 0}
			<Podium places={podium.places} winnerIds={podium.winnerIds} />
		{/if}

		<section class="flex flex-col gap-2">
			<h2 class="text-lg font-black">{m.final_ranking()}</h2>
			<ol aria-label={m.final_ranking()} class="flex flex-col gap-2">
				{#each podium.ranking as row (row.player.id)}
					<RankRow {row} isMe={row.player.id === view.youId} />
				{/each}
			</ol>
		</section>

		<RoundHistory rounds={history} meId={view.youId} />

		<section
			aria-label={m.support_link()}
			class="flex flex-col items-center gap-1 rounded-2xl bg-night-2 px-4 py-3 text-center"
		>
			<p class="text-sm text-cream-muted">{m.support_final_text()}</p>
			<SupportLink class="text-base text-cream" />
		</section>

		{#snippet footer()}
			<div class="flex items-center gap-2">
				{#if session.isHost}
					<Button
						tone="plain"
						size="md"
						block={false}
						class="shrink-0"
						onclick={() => session.leave()}
					>
						{m.final_home()}
					</Button>
					<Button tone="banana" class="min-w-0 flex-1" onclick={() => session.rematch()}>
						{m.final_rematch()}
					</Button>
				{:else}
					<p class="min-w-0 flex-1 text-center font-bold text-cream-muted" role="status">
						{m.final_waiting_rematch()}
					</p>
					<Button
						tone="plain"
						size="md"
						block={false}
						class="shrink-0"
						onclick={() => session.leave()}
					>
						{m.final_home()}
					</Button>
				{/if}
			</div>
		{/snippet}
	</Screen>
{/if}
