<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import { m } from '$lib/paraglide/messages';
	import { avatarOf } from './players';
	import type { PodiumPlace } from './podium';

	/**
	 * Podium: 2nd | 1st | 3rd steps. Ties share a step (several avatars); a
	 * tie for first leaves no second step. The medal, the place number and the
	 * score carry the meaning: the bounce-in only decorates.
	 */
	interface Props {
		places: PodiumPlace[];
		winnerIds: readonly string[];
	}
	let { places, winnerIds }: Props = $props();

	const order = [2, 1, 3];
	const columns = $derived(
		order.flatMap((n) => {
			const found = places.find((p) => p.place === n);
			return found ? [found] : [];
		})
	);
	const step = { 1: 'h-24 bg-banana', 2: 'h-16 bg-blueberry', 3: 'h-12 bg-tangerine' } as const;
	const delay = { 1: 450, 2: 150, 3: 0 } as const;
</script>

<ol aria-label={m.final_podium()} class="flex items-end justify-center gap-2" data-testid="podium">
	{#each columns as column (column.place)}
		{@const size = column.rows.length > 4 ? 32 : column.rows.length > 1 ? 40 : 60}
		<li
			class="flex min-w-0 animate-bounce-in flex-col items-center {column.place === 1
				? 'flex-[1.3]'
				: 'flex-1'}"
			style:animation-delay="{delay[column.place]}ms"
			data-place={column.place}
		>
			{#if column.place === 1}
				<span class="text-4xl" aria-hidden="true">🏆</span>
			{/if}
			<div class="flex flex-wrap justify-center gap-1 pb-1">
				{#each column.rows as row (row.player.id)}
					<span class={row.left ? 'opacity-60' : ''}>
						<AvatarBadge avatar={avatarOf(row.player)} {size} />
					</span>
				{/each}
			</div>
			<ul class="flex w-full flex-col items-center">
				{#each column.rows as row (row.player.id)}
					<li
						class="max-w-full text-center text-base leading-tight font-bold [overflow-wrap:anywhere] {row.left
							? 'text-cream-muted'
							: ''}"
						data-player={row.player.id}
					>
						{row.player.name}
						{#if winnerIds.includes(row.player.id)}
							<span class="sr-only">{m.final_winner_tag()}</span>
						{/if}
					</li>
				{/each}
			</ul>
			<p class="text-lg font-black tabular-nums">
				{m.final_points({ points: column.rows[0].total })}
			</p>
			<div
				class="mt-1 flex w-full items-start justify-center rounded-t-2xl pt-1 text-3xl text-night {step[
					column.place
				]}"
			>
				<span aria-hidden="true">{column.medal}</span>
				<span class="sr-only">{m.final_place({ place: column.place })}</span>
			</div>
		</li>
	{/each}
</ol>
