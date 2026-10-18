<script lang="ts">
	import { i18n, LOCALE_LABELS } from '$lib/i18n.svelte';
	import { m } from '$lib/paraglide/messages';
</script>

<div
	role="group"
	aria-label={m.language_label()}
	class="inline-flex gap-1 rounded-full bg-night-2 p-1 ring-1 ring-night-3"
>
	{#each i18n.locales as locale (locale)}
		<button
			type="button"
			lang={locale}
			aria-pressed={i18n.locale === locale}
			onclick={() => i18n.set(locale)}
			class="min-h-12 min-w-12 rounded-full px-4 text-base font-extrabold transition-colors {i18n.locale ===
			locale
				? 'bg-banana text-night'
				: 'text-cream-muted hover:text-cream'}"
		>
			{LOCALE_LABELS[locale]}
		</button>
	{/each}
</div>
