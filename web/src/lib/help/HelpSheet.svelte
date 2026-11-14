<script lang="ts">
	import { untrack } from 'svelte';
	import Button from '#lib/components/Button.svelte';
	import Sheet from '#lib/lobby/Sheet.svelte';
	import { m } from '#lib/paraglide/messages.js';
	import PointsBadge from '#lib/review/PointsBadge.svelte';
	import type { CellBadge } from '#lib/review/badge.ts';
	import { HELP_SECTIONS, type HelpSection } from './sections.ts';

	/**
	 * "How to play": a full-height sheet with the guide in four parts that open one at a time (an accordion,
	 * so it reads well on a narrow phone in every language). `section` is the part that starts open: the
	 * one for the screen the player came from. The sheet gives Android Back, Escape and focus handling.
	 */
	let { section = 'basics', onclose }: { section?: HelpSection; onclose: () => void } = $props();

	let current = $state<HelpSection | null>(untrack(() => section));

	const titles: Record<HelpSection, () => string> = {
		basics: () => m.help_section_basics(),
		playing: () => m.help_section_playing(),
		review: () => m.help_section_review(),
		points: () => m.help_section_points()
	};
	const icons: Record<HelpSection, string> = {
		basics: '🎯',
		playing: '✍️',
		review: '👀',
		points: '🏆'
	};
	const items: Partial<Record<HelpSection, (() => string)[]>> = {
		basics: [() => m.help_basics_1(), () => m.help_basics_2(), () => m.help_basics_3()],
		playing: [
			() => m.help_playing_1(),
			() => m.help_playing_2(),
			() => m.help_playing_3(),
			() => m.help_playing_4()
		],
		review: [
			() => m.help_review_1(),
			() => m.help_review_2(),
			() => m.help_review_3(),
			() => m.help_review_4(),
			() => m.help_review_5()
		]
	};

	const badge = (points: number, tone: CellBadge['tone'], kind: CellBadge['kind']): CellBadge => ({
		kind,
		points,
		tone,
		struck: false,
		chip: null
	});
	const points: { badge: CellBadge; text: () => string }[] = [
		{ badge: badge(10, 'lime', 'unique'), text: () => m.help_points_unique() },
		{ badge: badge(5, 'banana', 'shared'), text: () => m.help_points_shared() },
		{ badge: badge(0, 'grey', 'zero'), text: () => m.help_points_zero() }
	];

	function toggle(id: HelpSection) {
		current = current === id ? null : id;
	}
</script>

<Sheet title={m.help_title()} {onclose} size="full">
	<div class="flex flex-col gap-3 pb-2 text-left" data-testid="help-sheet">
		<p class="text-lg leading-snug text-cream-muted">{m.help_intro()}</p>

		{#each HELP_SECTIONS as id (id)}
			{@const open = current === id}
			<section class="rounded-2xl bg-night ring-1 {open ? 'ring-banana/60' : 'ring-night-3'}">
				<h3>
					<button
						type="button"
						id="help-heading-{id}"
						aria-expanded={open}
						aria-controls="help-panel-{id}"
						onclick={() => toggle(id)}
						class="flex min-h-14 w-full items-center gap-3 rounded-2xl px-4 text-left text-lg font-black transition active:bg-night-3"
					>
						<span aria-hidden="true">{icons[id]}</span>
						<span class="min-w-0 flex-1">{titles[id]()}</span>
						<span aria-hidden="true" class="text-cream-muted transition {open ? 'rotate-180' : ''}">
							▾
						</span>
					</button>
				</h3>
				<div id="help-panel-{id}" hidden={!open} class="flex flex-col gap-3 px-4 pb-4">
					{#if id === 'review'}
						<!-- the real buttons of the review screen, so they are easy to recognise -->
						<div class="flex flex-wrap gap-2" aria-hidden="true">
							<span
								class="inline-flex h-10 items-center gap-2 rounded-xl border-2 border-night-3 bg-night px-3 font-bold text-cream"
							>
								<span>👎</span>
								{m.review_reject()}
								<span class="rounded-full bg-night-3 px-2 text-sm font-black tabular-nums">0/2</span
								>
							</span>
							<span
								class="inline-flex h-10 items-center gap-2 rounded-xl border-2 border-grape bg-night px-3 font-black text-cream"
							>
								<span>≈</span>
								{m.review_same()}
							</span>
							<span
								class="inline-flex h-10 items-center gap-2 rounded-xl border-2 border-blueberry bg-night px-3 font-black text-cream"
							>
								<span>✨</span>
								{m.review_judge_ask()}
							</span>
						</div>
					{/if}
					{#if id === 'points'}
						<ul class="flex flex-col gap-2">
							{#each points as row (row.badge.points)}
								<li class="flex items-center gap-3">
									<PointsBadge badge={row.badge} />
									<span class="min-w-0 flex-1 leading-snug">{row.text()}</span>
								</li>
							{/each}
						</ul>
						<p class="leading-snug">{m.help_points_end()}</p>
					{:else}
						<ul class="flex list-disc flex-col gap-2 pl-5 leading-snug marker:text-banana">
							{#each items[id] ?? [] as text, i (i)}
								<li>{text()}</li>
							{/each}
						</ul>
					{/if}
				</div>
			</section>
		{/each}
	</div>

	{#snippet footer()}
		<Button tone="banana" onclick={onclose}>{m.help_got_it()}</Button>
	{/snippet}
</Sheet>
