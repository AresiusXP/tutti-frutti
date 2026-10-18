<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { CategoryRef } from '$lib/protocol';

	/** The selected categories, in game order, as small read-only chips (custom ones marked ✏️). */
	let { refs, labels }: { refs: readonly CategoryRef[]; labels: readonly string[] } = $props();
</script>

<ol class="flex flex-wrap gap-1.5">
	{#each refs as ref, i (ref.id ?? `c:${ref.custom}`)}
		<li
			class="flex items-center gap-1 rounded-full bg-night px-3 py-1 text-sm font-bold ring-1 ring-night-3"
		>
			<span class="text-cream-muted" aria-hidden="true">{i + 1}</span>
			<span>{labels[i] ?? ref.custom ?? ref.id}</span>
			{#if ref.custom}
				<span role="img" aria-label={m.lobby_cat_custom_mark()}>✏️</span>
			{/if}
		</li>
	{/each}
</ol>
