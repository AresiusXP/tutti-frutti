<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { CellBadge } from './badge';

	/** Points pill of one answer: 10 unique = lime, 5 shared = banana, 0 = grey; nothing for empty answers. */
	let { badge }: { badge: CellBadge } = $props();

	const tones = {
		lime: 'bg-lime text-night',
		banana: 'bg-banana text-night',
		grey: 'bg-night-3 text-cream-muted'
	} as const;
</script>

{#if badge.tone}
	<span
		class="inline-flex h-9 min-w-14 shrink-0 items-center justify-center rounded-full px-3 text-lg font-black tabular-nums {tones[
			badge.tone
		]}"
		data-testid="points-badge"
		data-kind={badge.kind}
		data-points={badge.points}
	>
		<span aria-hidden="true">{badge.points > 0 ? `+${badge.points}` : '0'}</span>
		<span class="sr-only">{m.review_points({ points: badge.points })}</span>
	</span>
{/if}
