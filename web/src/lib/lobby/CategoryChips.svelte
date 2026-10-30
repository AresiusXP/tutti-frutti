<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { CategoryRef } from '$lib/protocol';
	import SafetyButton from '$lib/safety/SafetyButton.svelte';

	/**
	 * The selected categories, in game order, as small read-only chips (custom ones marked ✏️ and, for
	 * the guests, reportable: somebody else typed them).
	 */
	let {
		refs,
		labels,
		reportable = false
	}: { refs: readonly CategoryRef[]; labels: readonly string[]; reportable?: boolean } = $props();
</script>

<ol class="flex flex-wrap gap-1.5">
	{#each refs as ref, i (ref.id ?? `c:${ref.custom}`)}
		<li
			class="flex items-center gap-1 rounded-full bg-night px-3 py-1 text-sm font-bold ring-1 ring-night-3 {reportable &&
			ref.custom
				? 'min-h-12'
				: ''}"
		>
			<span class="text-cream-muted" aria-hidden="true">{i + 1}</span>
			<span>{labels[i] ?? ref.custom ?? ref.id}</span>
			{#if ref.custom}
				<span role="img" aria-label={m.lobby_cat_custom_mark()}>✏️</span>
				{#if reportable}
					<SafetyButton
						request={{ start: 'label', category: i, label: labels[i] ?? ref.custom }}
						label={m.report_label_aria({ label: labels[i] ?? ref.custom })}
						class="-mr-3"
					/>
				{/if}
			{/if}
		</li>
	{/each}
</ol>
