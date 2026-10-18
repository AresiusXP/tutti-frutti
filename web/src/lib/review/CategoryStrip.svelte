<script lang="ts">
	import { m } from '$lib/paraglide/messages';

	interface Props {
		labels: readonly string[];
		current: number;
		/** Categories the viewer has seen (done dots). */
		visited: ReadonlySet<number>;
		/** The host can tap a dot to jump; for everybody else it is a progress indicator. */
		interactive: boolean;
		onjump?: (category: number) => void;
	}
	let { labels, current, visited, interactive, onjump }: Props = $props();

	const pill = (i: number) =>
		i === current
			? 'bg-banana text-night'
			: visited.has(i)
				? 'bg-night-3 text-cream'
				: 'border border-night-3 text-cream-muted';
</script>

<nav aria-label={m.review_progress()}>
	<ol class="flex gap-1">
		{#each labels as label, i (i)}
			<li class="min-w-0 flex-1">
				{#if interactive}
					<button
						type="button"
						class="flex h-12 w-full items-center justify-center"
						aria-label={m.review_jump({ n: i + 1, label })}
						aria-current={i === current ? 'step' : undefined}
						onclick={() => onjump?.(i)}
					>
						<span
							class="flex h-8 min-w-7 items-center justify-center rounded-full px-1 text-sm font-black {pill(
								i
							)}"
							aria-hidden="true">{i + 1}</span
						>
					</button>
				{:else}
					<span
						class="flex h-9 items-center justify-center"
						aria-current={i === current ? 'step' : undefined}
					>
						<span
							class="flex h-7 min-w-7 items-center justify-center rounded-full px-1 text-sm font-black {pill(
								i
							)}"
						>
							{i + 1}
						</span>
					</span>
				{/if}
			</li>
		{/each}
	</ol>
</nav>
