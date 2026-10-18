<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import { m } from '$lib/paraglide/messages';
	import type { Cell } from '$lib/protocol';
	import { classifyCell } from './badge';
	import { avatarOf } from './players';
	import PointsBadge from './PointsBadge.svelte';
	import type { RankRow } from './ranking';

	interface Props {
		row: RankRow;
		isMe: boolean;
		/** Per-category breakdown of the round; the row is expandable when given. */
		cells?: Cell[];
		labels?: readonly string[];
		letter?: string;
		/** Round points + rank arrow chips (summary); the final shows the plain ranking. */
		showRound?: boolean;
		expanded?: boolean;
		ontoggle?: () => void;
	}
	let {
		row,
		isMe,
		cells,
		labels = [],
		letter = '',
		showRound = false,
		expanded = false,
		ontoggle
	}: Props = $props();

	const expandable = $derived(!!cells && cells.length > 0 && !!ontoggle);
	const detailsId = $derived(`rank-details-${row.player.id}`);
	const delta = $derived(
		row.roundPoints > 0 ? `+${row.roundPoints}` : row.roundPoints === 0 && row.played ? '0' : ''
	);
</script>

{#snippet head()}
	<span
		class="w-8 shrink-0 text-center text-xl font-black tabular-nums {row.rank === null
			? 'text-cream-muted'
			: ''}"
	>
		{row.rank ?? '–'}
	</span>
	<AvatarBadge avatar={avatarOf(row.player)} size={40} />
	<span class="flex min-w-0 flex-1 flex-col items-start gap-0.5 text-left">
		<span class="flex max-w-full items-center gap-1.5">
			<span class="truncate text-base font-bold">{row.player.name}</span>
			{#if isMe}
				<span class="shrink-0 rounded-full bg-banana px-2 text-xs font-black text-night">
					{m.review_you()}
				</span>
			{/if}
		</span>
		<span class="flex flex-wrap items-center gap-1.5 text-sm">
			{#if showRound && delta}
				<span
					class="rounded-full px-2 font-black tabular-nums {row.roundPoints > 0
						? 'bg-lime text-night'
						: 'bg-night-3 text-cream-muted'}"
					data-testid="delta"
				>
					<span aria-hidden="true">{delta}</span>
					<span class="sr-only">{m.summary_round_points({ points: row.roundPoints })}</span>
				</span>
			{:else if showRound && !row.played && !row.left}
				<span class="text-cream-muted">{m.summary_joins_next()}</span>
			{/if}
			{#if showRound && row.change}
				<span
					class="font-black tabular-nums {row.change > 0 ? 'text-lime' : 'text-strawberry'}"
					data-testid="change"
				>
					<span aria-hidden="true">{row.change > 0 ? '▲' : '▼'}{Math.abs(row.change)}</span>
					<span class="sr-only">
						{row.change > 0
							? m.summary_up({ n: row.change })
							: m.summary_down({ n: Math.abs(row.change) })}
					</span>
				</span>
			{/if}
			{#if row.left}
				<span class="rounded-full bg-night-3 px-2 text-xs font-bold text-cream-muted">
					{m.review_left()}
				</span>
			{:else if !row.player.connected}
				<span class="inline-flex items-center gap-1 text-xs text-cream-muted">
					<span aria-hidden="true" class="size-2 rounded-full bg-cream-muted"></span>
					{m.review_offline()}
				</span>
			{/if}
		</span>
	</span>
	<span class="shrink-0 text-2xl font-black tabular-nums" data-testid="total">{row.total}</span>
{/snippet}

<li
	class="rounded-2xl border-2 bg-night-2 {isMe ? 'border-banana' : 'border-night-3'} {row.left
		? 'opacity-70'
		: ''}"
	data-player={row.player.id}
	data-rank={row.rank ?? ''}
>
	{#if expandable}
		<button
			type="button"
			class="flex min-h-16 w-full items-center gap-2 p-2.5 text-left"
			aria-expanded={expanded}
			aria-controls={detailsId}
			onclick={ontoggle}
		>
			{@render head()}
			<span aria-hidden="true" class="w-4 shrink-0 text-cream-muted">{expanded ? '▴' : '▾'}</span>
		</button>
		{#if expanded}
			<ul id={detailsId} class="flex flex-col gap-1 border-t-2 border-night-3 p-2.5">
				{#each cells ?? [] as cell, i (i)}
					{@const badge = classifyCell(cell)}
					<li class="flex items-center gap-2" data-category={i}>
						<span class="min-w-0 flex-1">
							<span class="block text-xs text-cream-muted">{labels[i] ?? ''}</span>
							<span
								class="block text-lg leading-tight font-bold [overflow-wrap:anywhere] {badge.struck
									? 'line-through decoration-strawberry decoration-2'
									: ''} {cell.raw === '' ? 'font-normal text-cream-muted' : ''}"
							>
								{cell.raw === '' ? m.review_no_answer() : cell.raw}
							</span>
							{#if badge.chip === 'wrong_letter'}
								<span class="text-xs text-cream-muted">{m.review_wrong_letter({ letter })}</span>
							{:else if badge.chip === 'rejected'}
								<span class="text-xs text-cream-muted">{m.review_rejected()}</span>
							{/if}
						</span>
						<PointsBadge {badge} />
					</li>
				{/each}
			</ul>
		{/if}
	{:else}
		<div class="flex min-h-16 items-center gap-2 p-2.5">
			{@render head()}
		</div>
	{/if}
</li>
