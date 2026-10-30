<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import { m } from '$lib/paraglide/messages';
	import type { AIVerdict, Cell, Player } from '$lib/protocol';
	import SafetyButton from '$lib/safety/SafetyButton.svelte';
	import { classifyCell } from './badge';
	import type { MergeUi } from './merge';
	import { avatarOf } from './players';
	import PointsBadge from './PointsBadge.svelte';
	import type { RejectTally } from './votes';

	interface Props {
		player: Player;
		isMe: boolean;
		/** Round number (1-based) and category index: what a report of this answer names. */
		round: number;
		category: number;
		answer: string;
		cell: Cell | undefined;
		letter: string;
		/** Other players whose answer counts the same as this one. */
		sharedWith: number;
		/** Reject votes on this answer. */
		tally: RejectTally;
		/** Counting voters (avatars next to the reject button). */
		voters: Player[];
		/** The viewer may vote on this answer (participant, not the author). */
		canVote: boolean;
		merge?: MergeUi;
		/** The AI's advisory opinion on this answer (never changes the points). */
		verdict?: AIVerdict;
		onreject?: () => void;
		onpick?: () => void;
	}
	let {
		player,
		isMe,
		round,
		category,
		answer,
		cell,
		letter,
		sharedWith,
		tally,
		voters,
		canVote,
		merge = { kind: 'none' },
		verdict,
		onreject,
		onpick
	}: Props = $props();

	const badge = $derived(classifyCell(cell));
	const empty = $derived(answer.trim() === '');
	const votable = $derived(
		canVote && !empty && badge.kind !== 'wrong_letter' && merge.kind === 'none'
	);
	const voterNames = $derived(voters.map((v) => v.name).join(', '));
	/** Dimmed: a struck answer, or one that cannot be picked while a merge is being set up. */
	const dim = $derived(merge.kind === 'dim' || (badge.struck && merge.kind === 'none'));
	/**
	 * What the reject button shows ("Rechazar 0/2"). Its accessible name starts with exactly this
	 * (WCAG 2.5.3 label in name) and then says whose answer it is.
	 */
	const rejectLabel = $derived(`${m.review_reject()} ${tally.votes}/${tally.needed}`);
	const who = $derived({ id: player.id, name: player.name });
	/** The AI's note as the viewer reads it (what a report of it quotes). */
	const verdictText = $derived(
		verdict
			? `${verdict.verdict === 'valid' ? m.review_judge_valid() : m.review_judge_doubtful()}${
					verdict.reason ? `: ${verdict.reason}` : ''
				}`
			: ''
	);
</script>

<li
	class="rounded-2xl border-2 bg-night-2 p-3 {merge.kind === 'picked'
		? 'border-grape ring-4 ring-grape/40'
		: isMe
			? 'border-banana/70'
			: 'border-night-3'}"
	data-player={player.id}
	data-kind={badge.kind}
>
	<!-- Only the name and the answer are dimmed: opacity on the whole card also dims the chip and the buttons, and the strawberry ones then fall below 4.5:1. -->
	<div class="flex items-center gap-2" class:opacity-80={dim}>
		<AvatarBadge avatar={avatarOf(player)} size={36} />
		<div class="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-0.5">
			<span class="max-w-full truncate text-base font-bold" class:text-cream-muted={player.left}>
				{player.name}
			</span>
			{#if isMe}
				<span class="rounded-full bg-banana px-2 text-xs font-black text-night">
					{m.review_you()}
				</span>
			{/if}
			{#if player.left}
				<span class="rounded-full bg-night-3 px-2 text-xs font-bold text-cream-muted">
					{m.review_left()}
				</span>
			{:else if !player.connected}
				<span class="inline-flex items-center gap-1 text-xs text-cream-muted">
					<span aria-hidden="true" class="size-2 rounded-full bg-cream-muted"></span>
					{m.review_offline()}
				</span>
			{/if}
		</div>
		<PointsBadge {badge} />
		<!-- Others' answers only (a report names the author); nothing is offered for a hidden answer: cards exist from the review on. -->
		<SafetyButton
			request={{
				start: 'person',
				person: who,
				answer: empty ? undefined : { round, category, text: answer }
			}}
			label={empty
				? m.report_player_aria({ name: player.name })
				: m.report_answer_aria({ name: player.name })}
			class="-mr-1"
		/>
	</div>

	<p
		class="mt-2 text-2xl leading-tight font-black [overflow-wrap:anywhere] break-words {empty
			? 'text-lg font-normal text-cream-muted'
			: ''} {badge.struck ? 'line-through decoration-strawberry decoration-4' : ''} {dim
			? 'opacity-80'
			: ''}"
		data-testid="answer"
	>
		{empty ? m.review_no_answer() : answer}
	</p>

	{#if badge.chip || sharedWith > 0}
		<div class="mt-1.5 flex flex-wrap gap-1.5">
			{#if badge.chip === 'wrong_letter'}
				<span class="rounded-full bg-night-3 px-2.5 py-0.5 text-sm font-bold text-cream">
					{m.review_wrong_letter({ letter })}
				</span>
			{:else if badge.chip === 'rejected'}
				<span class="rounded-full bg-strawberry px-2.5 py-0.5 text-sm font-black text-night">
					{m.review_rejected()}
				</span>
			{/if}
			{#if sharedWith > 0 && badge.kind === 'shared'}
				<span class="rounded-full bg-night-3 px-2.5 py-0.5 text-sm text-cream">
					{sharedWith === 1 ? m.review_shared_one() : m.review_shared_many({ count: sharedWith })}
				</span>
			{/if}
		</div>
	{/if}

	{#if verdict}
		{@const valid = verdict.verdict === 'valid'}
		<div
			class="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1"
			data-testid="verdict"
			data-verdict={valid ? 'valid' : 'doubtful'}
		>
			<span
				class="inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-sm font-black text-night {valid
					? 'bg-lime'
					: 'bg-tangerine'}"
			>
				<span aria-hidden="true">{valid ? '✓' : '?'}</span>
				{valid ? m.review_judge_valid() : m.review_judge_doubtful()}
			</span>
			{#if verdict.reason}
				<span class="min-w-0 flex-1 text-sm [overflow-wrap:anywhere] text-cream-muted">
					{verdict.reason}
				</span>
			{/if}
			<SafetyButton
				request={{
					start: 'verdict',
					person: who,
					verdict: { round, category, text: verdictText }
				}}
				label={m.report_verdict_aria({ name: player.name })}
				class="ml-auto"
			/>
		</div>
	{/if}

	{#if votable}
		<div class="mt-2 flex items-center gap-2">
			<button
				type="button"
				class="flex h-12 flex-1 items-center justify-center gap-2 rounded-xl border-2 px-3 font-bold transition active:translate-y-0.5 {tally.mine
					? 'border-strawberry bg-strawberry text-night'
					: 'border-night-3 bg-night text-cream'}"
				aria-pressed={tally.mine}
				aria-label={m.review_reject_aria({ label: rejectLabel, name: player.name })}
				onclick={() => onreject?.()}
			>
				<span aria-hidden="true">👎</span>
				<span aria-hidden="true">{m.review_reject()}</span>
				<span
					aria-hidden="true"
					class="rounded-full px-2 text-sm font-black tabular-nums {tally.mine
						? 'bg-cream/25'
						: 'bg-night-3'}"
				>
					{tally.votes}/{tally.needed}
				</span>
			</button>
			{#if voters.length > 0}
				<span
					role="img"
					aria-label={m.review_voters_aria({ names: voterNames })}
					class="flex shrink-0 -space-x-2"
				>
					{#each voters as voter (voter.id)}
						<span class="rounded-full ring-2 ring-night-2">
							<AvatarBadge avatar={avatarOf(voter)} size={28} />
						</span>
					{/each}
				</span>
			{/if}
		</div>
	{:else if merge.kind === 'pick' || merge.kind === 'picked'}
		<button
			type="button"
			class="mt-2 flex h-12 w-full items-center justify-center rounded-xl font-black transition active:translate-y-0.5 {merge.kind ===
			'picked'
				? 'border-2 border-grape bg-night text-cream'
				: 'bg-grape text-night'}"
			aria-label={merge.kind === 'picked'
				? m.review_same_unpick_aria({ name: player.name })
				: m.review_same_pick_aria({ name: player.name })}
			onclick={() => onpick?.()}
		>
			<span aria-hidden="true">
				{merge.kind === 'picked' ? `✓ ${m.review_same_unpick()}` : m.review_same_pick()}
			</span>
		</button>
	{:else if merge.kind === 'note'}
		<p class="mt-2 text-sm font-bold text-cream-muted">
			{merge.note === 'already' ? m.review_same_already() : m.review_same_pending()}
		</p>
	{/if}
</li>
