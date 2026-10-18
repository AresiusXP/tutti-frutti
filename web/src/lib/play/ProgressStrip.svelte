<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import { m } from '$lib/paraglide/messages';
	import type { ProgressEntry } from './progress';

	let { entries, size = 28 }: { entries: ProgressEntry[]; size?: number } = $props();

	function describe(e: ProgressEntry): string {
		const params = { name: e.you ? m.play_you() : e.name, filled: e.filled, total: e.total };
		return e.offline ? m.play_progress_offline(params) : m.play_progress(params);
	}
</script>

<ul class="flex flex-wrap gap-x-1.5 gap-y-1" aria-label={m.play_progress_label()}>
	{#each entries as e (e.id)}
		<li class="flex w-9 flex-col items-center gap-0.5">
			<span
				class="rounded-full p-[3px]"
				style:background="conic-gradient(var(--color-lime) {e.percent}%, var(--color-night-3) 0)"
				aria-hidden="true"
			>
				<span class="block rounded-full {e.offline ? 'opacity-50 grayscale' : ''}">
					<AvatarBadge avatar={e.avatar} {size} />
				</span>
			</span>
			<span
				class="text-xs leading-none font-bold tabular-nums {e.done
					? 'text-lime'
					: e.you
						? 'text-banana'
						: 'text-cream-muted'}"
				aria-hidden="true"
			>
				{e.filled}/{e.total}
			</span>
			<span class="sr-only">{describe(e)}</span>
		</li>
	{/each}
</ul>
