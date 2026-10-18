<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import type { Settings } from '$lib/protocol';
	import CategoryChips from './CategoryChips.svelte';
	import { MAX_CATEGORIES } from './labels';
	import { playableLetters } from './letters';

	const langNames = {
		es: () => m.lobby_lang_es(),
		en: () => m.lobby_lang_en(),
		nl: () => m.lobby_lang_nl()
	} as const;

	/** What everybody but the host sees: the settings as read-only chips. */
	interface Props {
		settings: Settings;
		labels: readonly string[];
		hostName: string;
	}
	let { settings, labels, hostName }: Props = $props();

	const letters = $derived(playableLetters(settings.lang, settings.excludeHardLetters).length);
	const chips = $derived([
		langNames[settings.lang](),
		settings.excludeHardLetters
			? m.lobby_summary_hard_on({ count: letters })
			: m.lobby_summary_hard_off({ count: letters }),
		settings.rounds === 0
			? m.lobby_summary_rounds_unlimited()
			: m.lobby_summary_rounds({ n: settings.rounds }),
		settings.timeLimitSeconds === 0
			? m.lobby_summary_time_off()
			: m.lobby_summary_time({ n: settings.timeLimitSeconds }),
		settings.stopVote ? m.lobby_summary_stop_on() : m.lobby_summary_stop_off()
	]);
</script>

<section
	aria-labelledby="lobby-settings-title"
	class="flex flex-col gap-3 rounded-3xl bg-night-2/80 p-3 ring-1 ring-night-3"
>
	<div class="flex flex-wrap items-baseline justify-between gap-x-2 px-1">
		<h2 id="lobby-settings-title" class="text-lg font-black">{m.lobby_settings_title()}</h2>
		<p class="text-sm font-bold text-cream-muted">{m.lobby_configured_by({ name: hostName })}</p>
	</div>

	<ul class="flex flex-wrap gap-1.5" aria-label={m.lobby_settings_title()}>
		{#each chips as chip (chip)}
			<li class="rounded-full bg-night px-3 py-1.5 text-sm font-bold ring-1 ring-night-3">
				{chip}
			</li>
		{/each}
	</ul>

	<div class="flex flex-col gap-2">
		<div class="flex items-baseline justify-between gap-2 px-1">
			<h3 class="text-sm font-bold text-cream-muted">{m.lobby_cats_title()}</h3>
			<span class="text-sm font-extrabold">{settings.categories.length}/{MAX_CATEGORIES}</span>
		</div>
		<CategoryChips refs={settings.categories} {labels} />
	</div>
</section>
