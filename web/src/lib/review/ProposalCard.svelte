<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import { m } from '$lib/paraglide/messages';
	import type { Player, ProposalView } from '$lib/protocol';
	import SafetyButton from '$lib/safety/SafetyButton.svelte';
	import { avatarOf } from './players';
	import type { MergeTally } from './votes';

	interface Props {
		proposal: ProposalView;
		a: Player;
		b: Player;
		answerA: string;
		answerB: string;
		/** Round number (1-based) and category index: what a report of one of the answers names. */
		round: number;
		category: number;
		tally: MergeTally;
		/** The viewer may vote (participant). */
		canVote: boolean;
		onvote?: (yes: boolean) => void;
	}
	let { proposal, a, b, answerA, answerB, round, category, tally, canVote, onvote }: Props =
		$props();
</script>

<li
	class="rounded-2xl border-2 bg-night-2 p-3 {tally.passed ? 'border-lime' : 'border-grape'}"
	data-proposal={proposal.id}
	data-passed={tally.passed}
>
	<div class="flex items-center gap-2">
		<span class="flex shrink-0 -space-x-2">
			<span class="rounded-full ring-2 ring-night-2">
				<AvatarBadge avatar={avatarOf(a)} size={32} />
			</span>
			<span class="rounded-full ring-2 ring-night-2">
				<AvatarBadge avatar={avatarOf(b)} size={32} />
			</span>
		</span>
		<p class="min-w-0 flex-1 text-base leading-tight font-bold [overflow-wrap:anywhere]">
			{m.review_proposal_title({ a: a.name, b: b.name })}
		</p>
		{#each [{ player: a, text: answerA }, { player: b, text: answerB }] as entry (entry.player.id)}
			{#if entry.text.trim() !== ''}
				<SafetyButton
					request={{
						start: 'person',
						person: { id: entry.player.id, name: entry.player.name },
						answer: { round, category, text: entry.text }
					}}
					label={m.report_proposal_aria({ name: entry.player.name })}
				/>
			{/if}
		{/each}
	</div>
	<p class="mt-1 text-lg font-bold [overflow-wrap:anywhere] break-words">
		{m.review_proposal_answers({ a: answerA, b: answerB })}
	</p>

	<div class="mt-2 flex items-center gap-2">
		{#if canVote}
			<button
				type="button"
				class="h-12 flex-1 rounded-xl border-2 text-lg font-black transition active:translate-y-0.5 {tally.mine ===
				true
					? 'border-lime bg-lime text-night'
					: 'border-night-3 bg-night text-cream'}"
				aria-pressed={tally.mine === true}
				aria-label={m.review_vote_yes_aria({ a: a.name, b: b.name })}
				onclick={() => onvote?.(true)}
			>
				<span aria-hidden="true">{m.review_yes()}</span>
			</button>
			<button
				type="button"
				class="h-12 flex-1 rounded-xl border-2 text-lg font-black transition active:translate-y-0.5 {tally.mine ===
				false
					? 'border-strawberry bg-strawberry text-night'
					: 'border-night-3 bg-night text-cream'}"
				aria-pressed={tally.mine === false}
				aria-label={m.review_vote_no_aria({ a: a.name, b: b.name })}
				onclick={() => onvote?.(false)}
			>
				<span aria-hidden="true">{m.review_no()}</span>
			</button>
		{/if}
	</div>

	<p class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
		<span class="font-bold tabular-nums" data-testid="tally">
			{m.review_tally({ yes: tally.yes, total: tally.total })}
		</span>
		<span
			class="rounded-full px-2.5 py-0.5 font-black text-night {tally.passed
				? 'bg-lime'
				: 'bg-banana'}"
		>
			{tally.passed ? m.review_proposal_passed() : m.review_proposal_open()}
		</span>
	</p>
</li>
