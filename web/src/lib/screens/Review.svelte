<script lang="ts">
	import { untrack } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import Button from '$lib/components/Button.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import type { RoomSession } from '$lib/game/session';
	import { m } from '$lib/paraglide/messages';
	import AnswerCard from '$lib/review/AnswerCard.svelte';
	import CategoryStrip from '$lib/review/CategoryStrip.svelte';
	import ConfirmBar from '$lib/review/ConfirmBar.svelte';
	import { answerGroups } from '$lib/review/groups';
	import {
		firstPickable,
		MERGE_IDLE,
		pairBlocker,
		stepMerge,
		type MergeAction,
		type MergeContext,
		type MergeState,
		type MergeUi
	} from '$lib/review/merge';
	import { playerName } from '$lib/review/players';
	import ProposalCard from '$lib/review/ProposalCard.svelte';
	import { toasts } from '$lib/toast.svelte';
	import { mergeTally, rejectTally } from '$lib/review/votes';

	/**
	 * Review: everybody sees the same category (the host moves it with ◀ ▶,
	 * clamped at both ends) and one card per answer with its live points.
	 * Participants vote to reject other players' answers and propose merges
	 * ("Son iguales"); the server recomputes the result after each vote.
	 */
	let { session }: { session: RoomSession } = $props();

	const view = $derived(session.view);
	const round = $derived(view?.round);
	const labels = $derived(view?.categoryLabels ?? []);
	const total = $derived(view?.settings.categories.length ?? 0);
	const category = $derived(round?.reviewCategory ?? 0);
	const players = $derived(view?.players ?? []);
	const meId = $derived(view?.youId ?? '');
	const hostName = $derived(playerName(players, view?.hostId ?? ''));
	const participant = $derived(session.isParticipant);
	const voter = $derived(round?.reviewVoters.includes(meId) ?? false);
	const proposals = $derived(
		(round?.proposals ?? [])
			.filter((proposal) => proposal.category === category)
			.flatMap((proposal) => {
				const a = players.find((p) => p.id === proposal.playerA);
				const b = players.find((p) => p.id === proposal.playerB);
				return a && b ? [{ proposal, a, b }] : [];
			})
	);

	const mergeCtx: MergeContext = $derived({
		category,
		participants: round?.participants ?? [],
		result: round?.result,
		proposals: round?.proposals ?? []
	});
	const groups = $derived(
		answerGroups(mergeCtx.participants, mergeCtx.result, mergeCtx.proposals, category)
	);
	const pickable = $derived(firstPickable(mergeCtx));

	const verdicts = $derived(
		(round?.verdicts ?? []).filter((verdict) => verdict.category === category)
	);
	const checking = $derived((round?.judgePending ?? []).includes(category));
	const judged = $derived(verdicts.length > 0);
	/** The host tapped "Ask the AI" and the server has not answered yet (no double asks). */
	let asked = $state<number | null>(null);
	const asking = $derived(checking || asked === category);
	const canAsk = $derived(session.isHost && session.canJudge);

	function askJudge() {
		if (asking || judged) return;
		asked = category;
		session.askJudge(category);
	}

	$effect(() => {
		if (checking || judged) asked = null;
	});
	$effect(() => {
		void category;
		asked = null;
	});
	$effect(() => {
		if (session.status !== 'connected') asked = null;
	});
	$effect(() => {
		if (session.lastError) asked = null;
	});

	// Only the host (the one who asked) hears that the AI failed; everybody else just sees the spinner go away.
	let seenEvent = untrack(() => session.events.at(-1)?.seq ?? 0);
	$effect(() => {
		const events = session.events;
		untrack(() => {
			for (const event of events) {
				if (event.seq <= seenEvent) continue;
				seenEvent = event.seq;
				if (event.type !== 'judge_failed') continue;
				asked = null;
				if (!session.isHost) continue;
				toasts.show(event.reason === 'busy' ? m.review_judge_busy() : m.review_judge_error());
			}
		});
	});

	const visited = new SvelteSet<number>();
	$effect(() => {
		visited.add(category);
	});

	let mergeState = $state<MergeState>(MERGE_IDLE);
	let confirmFinish = $state(false);

	$effect(() => {
		void category;
		mergeState = MERGE_IDLE;
	});
	$effect(() => {
		if (mergeState.step === 'second' && !pickable.includes(mergeState.first)) {
			mergeState = MERGE_IDLE;
		}
	});

	function merge(action: MergeAction) {
		const next = stepMerge(mergeState, action, mergeCtx);
		mergeState = next.state;
		if (next.propose) session.proposeMerge(category, next.propose.a, next.propose.b);
	}

	function mergeUi(id: string): MergeUi {
		if (mergeState.step === 'idle') return { kind: 'none' };
		if (mergeState.step === 'first') {
			return pickable.includes(id) ? { kind: 'pick' } : { kind: 'dim' };
		}
		if (id === mergeState.first) return { kind: 'picked' };
		const blocker = pairBlocker(mergeCtx, mergeState.first, id);
		if (blocker === null) return { kind: 'pick' };
		if (blocker === 'already_same') return { kind: 'note', note: 'already' };
		if (blocker === 'proposal_exists') return { kind: 'note', note: 'pending' };
		return { kind: 'dim' };
	}

	const pickedAnswer = $derived(
		mergeState.step === 'second' ? (round?.answers[mergeState.first]?.[category] ?? '') : ''
	);

	function reject(id: string) {
		const mine = rejectTally(
			round?.reviewVoters ?? [],
			round?.rejectVotes ?? [],
			category,
			id,
			meId
		).mine;
		if (mine) session.voteReject(category, id, true);
		else session.voteReject(category, id);
	}

	function go(target: number) {
		if (target < 0 || target >= total || target === category) return;
		session.reviewNav(target);
	}

	function finish() {
		if (visited.size < total) confirmFinish = true;
		else session.finishReview();
	}

	const arrow =
		'flex h-14 w-12 shrink-0 items-center justify-center rounded-2xl bg-night-3 text-xl font-black text-cream shadow-pop transition active:translate-y-1 active:shadow-none disabled:bg-night-2 disabled:text-cream-muted disabled:shadow-none';
</script>

{#if view && round}
	<Screen label={m.review_label({ round: round.number })}>
		{#snippet header()}
			<div class="flex items-center gap-3 pt-1">
				<span
					class="flex size-14 shrink-0 items-center justify-center rounded-2xl bg-banana text-4xl font-black text-night shadow-pop-sm"
					role="img"
					aria-label={m.review_letter({ letter: round.letter })}
				>
					{round.letter}
				</span>
				<h1
					class="min-w-0 flex-1 text-2xl leading-tight font-black [overflow-wrap:anywhere]"
					aria-live="polite"
					data-testid="category-title"
				>
					{m.review_category_pos({
						label: labels[category] ?? '',
						n: category + 1,
						total
					})}
				</h1>
			</div>
			<CategoryStrip
				{labels}
				current={category}
				{visited}
				interactive={session.isHost}
				onjump={go}
			/>
		{/snippet}

		{#if !participant}
			<p class="rounded-2xl bg-night-2 p-3 text-center text-cream-muted" role="status">
				{m.review_spectator()}
			</p>
		{/if}

		{#if proposals.length > 0}
			<section aria-label={m.review_proposals()} class="flex flex-col gap-2">
				<ul class="flex flex-col gap-2">
					{#each proposals as { proposal, a, b } (proposal.id)}
						<ProposalCard
							{proposal}
							{a}
							{b}
							answerA={round.answers[proposal.playerA]?.[category] ?? ''}
							answerB={round.answers[proposal.playerB]?.[category] ?? ''}
							round={round.number}
							{category}
							tally={mergeTally(proposal, round.reviewVoters, meId)}
							canVote={voter}
							onvote={(yes) => session.voteMerge(proposal.id, yes)}
						/>
					{/each}
				</ul>
			</section>
		{/if}

		<section aria-label={m.review_answers()} class="flex flex-col gap-2">
			{#if voter && (pickable.length > 0 || mergeState.step !== 'idle')}
				<div class="flex justify-end">
					<button
						type="button"
						class="h-12 rounded-xl border-2 px-4 font-black transition active:translate-y-0.5 {mergeState.step !==
						'idle'
							? 'border-grape bg-grape text-night'
							: 'border-grape bg-night text-cream'}"
						aria-pressed={mergeState.step !== 'idle'}
						onclick={() => merge({ type: 'toggle' })}
					>
						<span aria-hidden="true">≈</span>
						{m.review_same()}
					</button>
				</div>
			{/if}
			{#if canAsk || checking || judged}
				<div class="flex flex-col gap-1" data-testid="judge">
					{#if canAsk && !judged}
						<button
							type="button"
							class="flex h-12 w-full items-center justify-center gap-2 rounded-xl border-2 border-blueberry px-4 font-black transition active:translate-y-0.5 {asking
								? 'bg-night-2 text-cream-muted'
								: 'bg-night text-cream'}"
							disabled={asking}
							aria-busy={asking}
							onclick={askJudge}
						>
							<span aria-hidden="true">{asking ? '⏳' : '✨'}</span>
							{asking ? m.review_judge_working() : m.review_judge_ask()}
						</button>
						<span class="sr-only" role="status">{asking ? m.review_judge_working() : ''}</span>
					{:else if checking}
						<p
							class="flex min-h-8 items-center justify-center gap-2 text-sm font-bold text-cream-muted"
							role="status"
							aria-busy="true"
						>
							<span aria-hidden="true">⏳</span>
							{m.review_judge_working()}
						</p>
					{:else if judged}
						<p
							class="flex min-h-8 flex-wrap items-center justify-center gap-x-2 text-sm text-cream-muted"
							role="status"
						>
							{#if canAsk}
								<span class="font-bold">
									<span aria-hidden="true">✓</span>
									{m.review_judge_done()}
								</span>
							{/if}
							<span>{m.review_judge_caption()}</span>
						</p>
					{/if}
				</div>
			{/if}
			<ul class="flex flex-col gap-2">
				{#each round.participants as id (id)}
					{@const player = players.find((p) => p.id === id)}
					{#if player}
						{@const tally = rejectTally(round.reviewVoters, round.rejectVotes, category, id, meId)}
						<AnswerCard
							{player}
							isMe={id === meId}
							round={round.number}
							{category}
							answer={round.answers[id]?.[category] ?? ''}
							cell={round.result?.players[id]?.cells[category]}
							letter={round.letter}
							sharedWith={groups.sharedWith(id)}
							{tally}
							voters={tally.voters
								.map((v) => players.find((p) => p.id === v))
								.filter((p) => p !== undefined)}
							canVote={voter && id !== meId}
							merge={mergeUi(id)}
							verdict={verdicts.find((v) => v.player === id)}
							onreject={() => reject(id)}
							onpick={() => merge({ type: 'pick', player: id })}
						/>
					{/if}
				{/each}
			</ul>
		</section>

		{#snippet footer()}
			{#if mergeState.step !== 'idle'}
				<div
					class="flex items-center gap-2 rounded-2xl bg-grape p-2 pl-3 text-night"
					role="status"
					data-testid="merge-banner"
				>
					<p class="min-w-0 flex-1 leading-tight font-black [overflow-wrap:anywhere]">
						{mergeState.step === 'first'
							? m.review_same_first()
							: m.review_same_second({ answer: pickedAnswer })}
					</p>
					<button
						type="button"
						class="h-12 shrink-0 rounded-xl bg-night px-4 font-black text-cream"
						onclick={() => merge({ type: 'cancel' })}
					>
						{m.review_same_cancel()}
					</button>
				</div>
			{/if}
			{#if confirmFinish}
				<ConfirmBar
					message={m.review_finish_unseen({ seen: visited.size, total })}
					yes={m.review_finish_anyway()}
					no={m.review_keep_reviewing()}
					onyes={() => {
						confirmFinish = false;
						session.finishReview();
					}}
					onno={() => (confirmFinish = false)}
				/>
			{:else if session.isHost}
				<div class="flex items-center gap-2">
					<button
						type="button"
						class={arrow}
						aria-label={m.review_prev()}
						disabled={category <= 0}
						onclick={() => go(category - 1)}
					>
						<svg aria-hidden="true" viewBox="0 0 24 24" class="size-6" fill="currentColor">
							<path d="M15.5 4.5 7 12l8.5 7.5z" />
						</svg>
					</button>
					<Button tone="lime" class="min-w-0 flex-1 px-2! text-xl" onclick={finish}>
						{m.review_finish()}
					</Button>
					<button
						type="button"
						class={arrow}
						aria-label={m.review_next()}
						disabled={category >= total - 1}
						onclick={() => go(category + 1)}
					>
						<svg aria-hidden="true" viewBox="0 0 24 24" class="size-6" fill="currentColor">
							<path d="M8.5 4.5 17 12l-8.5 7.5z" />
						</svg>
					</button>
				</div>
			{:else}
				<p class="py-2 text-center font-bold text-cream-muted" role="status">
					{m.review_host_controls({ name: hostName })}
				</p>
			{/if}
		{/snippet}
	</Screen>
{/if}
