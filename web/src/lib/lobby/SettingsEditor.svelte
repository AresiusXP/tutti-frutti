<script lang="ts">
	import Button from '$lib/components/Button.svelte';
	import { m } from '$lib/paraglide/messages';
	import type { Lang, Settings } from '$lib/protocol';
	import CategoryChips from './CategoryChips.svelte';
	import CategoryPicker from './CategoryPicker.svelte';
	import { MAX_CATEGORIES } from './labels';
	import { HARD_LETTERS, maxRounds, playableLetters } from './letters';
	import type { Rng } from './presets';
	import Segmented from './Segmented.svelte';
	import Sheet from './Sheet.svelte';
	import {
		DEFAULT_ROUNDS,
		TIME_LIMITS,
		withExcludeHard,
		withLang,
		withRounds,
		withTimeLimit
	} from './settings';

	/** The host's settings card: language, hard letters, rounds, time limit and the categories picker. */
	interface Props {
		settings: Settings;
		labels: readonly string[];
		onchange: (next: Settings) => void;
		/** The server refused the last edit. */
		failed?: boolean;
		rng?: Rng;
	}
	let { settings, labels, onchange, failed = false, rng }: Props = $props();

	let pickerOpen = $state(false);
	let lastFinite = DEFAULT_ROUNDS;

	const max = $derived(maxRounds(settings.lang, settings.excludeHardLetters));
	const unlimited = $derived(settings.rounds === 0);
	const letters = $derived(playableLetters(settings.lang, settings.excludeHardLetters));
	const hardList = $derived(HARD_LETTERS[settings.lang].join(' '));

	const langOptions = $derived<{ value: Lang; label: string }[]>([
		{ value: 'es', label: m.lobby_lang_es() },
		{ value: 'en', label: m.lobby_lang_en() },
		{ value: 'nl', label: m.lobby_lang_nl() }
	]);
	const timeOptions = $derived(
		TIME_LIMITS.map((seconds) => ({
			value: seconds as number,
			label: seconds === 0 ? m.lobby_time_off() : m.lobby_time_secs({ n: seconds }),
			ariaLabel: seconds === 0 ? m.lobby_time_off_label() : m.lobby_time_secs_label({ n: seconds })
		}))
	);

	function toggleUnlimited() {
		if (unlimited) {
			onchange(withRounds(settings, lastFinite));
		} else {
			lastFinite = settings.rounds;
			onchange(withRounds(settings, 0));
		}
	}

	const stepper =
		'flex size-12 items-center justify-center rounded-xl text-2xl font-black disabled:opacity-30';
</script>

<section
	aria-labelledby="lobby-settings-title"
	class="flex flex-col gap-3 rounded-3xl bg-night-2/80 p-3 ring-1 ring-night-3"
>
	<h2 id="lobby-settings-title" class="px-1 text-lg font-black">{m.lobby_settings_title()}</h2>

	{#if failed}
		<p
			role="alert"
			class="rounded-xl bg-strawberry/15 px-3 py-2 text-sm font-bold ring-1 ring-strawberry"
		>
			{m.lobby_settings_error()}
		</p>
	{/if}

	<div class="flex flex-col gap-1">
		<span aria-hidden="true" class="px-1 text-sm font-bold text-cream-muted"
			>{m.lobby_lang_label()}</span
		>
		<Segmented
			label={m.lobby_lang_label()}
			options={langOptions}
			value={settings.lang}
			onchange={(lang) => onchange(withLang(settings, lang))}
		/>
		<p class="px-1 text-xs text-cream-muted">{m.lobby_lang_hint()}</p>
	</div>

	<button
		type="button"
		role="switch"
		aria-checked={settings.excludeHardLetters}
		aria-labelledby="hard-label"
		aria-describedby="hard-desc"
		onclick={() => onchange(withExcludeHard(settings, !settings.excludeHardLetters))}
		class="flex min-h-14 items-center gap-3 rounded-2xl bg-night px-3 py-2 text-left ring-2 ring-night-3"
	>
		<span class="flex min-w-0 flex-1 flex-col">
			<span id="hard-label" class="font-extrabold">{m.lobby_hard_label()}</span>
			<span id="hard-desc" class="text-sm text-cream-muted">
				{#if settings.excludeHardLetters}
					{m.lobby_hard_on({ letters: hardList, count: letters.length })}
				{:else}
					{m.lobby_hard_off({ count: letters.length })}
				{/if}
			</span>
		</span>
		<span
			aria-hidden="true"
			class="relative h-8 w-14 shrink-0 rounded-full transition-colors {settings.excludeHardLetters
				? 'bg-lime'
				: 'bg-night-3'}"
		>
			<span
				class="absolute top-1 size-6 rounded-full bg-cream shadow transition-all {settings.excludeHardLetters
					? 'left-7'
					: 'left-1'}"
			></span>
		</span>
	</button>

	<div role="group" aria-labelledby="rounds-label" class="flex flex-col gap-1">
		<div class="flex items-baseline justify-between gap-2 px-1">
			<span id="rounds-label" class="text-sm font-bold text-cream-muted">
				{m.lobby_rounds_label()}
			</span>
			<span class="text-xs text-cream-muted">{m.lobby_rounds_max({ max })}</span>
		</div>
		<div class="flex items-center gap-2">
			<div class="flex shrink-0 items-center rounded-2xl bg-night p-0.5 ring-2 ring-night-3">
				<button
					type="button"
					aria-label={m.lobby_rounds_less()}
					disabled={unlimited || settings.rounds <= 1}
					onclick={() => onchange(withRounds(settings, settings.rounds - 1))}
					class={stepper}
				>
					<span aria-hidden="true">−</span>
				</button>
				<output
					aria-live="polite"
					aria-label={unlimited
						? m.lobby_rounds_unlimited()
						: m.lobby_rounds_value({ n: settings.rounds })}
					class="w-12 text-center text-2xl font-black"
				>
					{unlimited ? '∞' : settings.rounds}
				</output>
				<button
					type="button"
					aria-label={m.lobby_rounds_more()}
					disabled={unlimited || settings.rounds >= max}
					onclick={() => onchange(withRounds(settings, settings.rounds + 1))}
					class={stepper}
				>
					<span aria-hidden="true">+</span>
				</button>
			</div>
			<button
				type="button"
				aria-pressed={unlimited}
				onclick={toggleUnlimited}
				class="min-h-12 flex-1 rounded-2xl px-3 text-base font-extrabold ring-2 transition-colors {unlimited
					? 'bg-banana text-night ring-banana'
					: 'bg-night text-cream-muted ring-night-3'}"
			>
				<span aria-hidden="true">∞</span>
				{m.lobby_rounds_unlimited()}
			</button>
		</div>
	</div>

	<div class="flex flex-col gap-1">
		<span aria-hidden="true" class="px-1 text-sm font-bold text-cream-muted"
			>{m.lobby_time_label()}</span
		>
		<Segmented
			label={m.lobby_time_label()}
			options={timeOptions}
			value={settings.timeLimitSeconds}
			onchange={(seconds) => onchange(withTimeLimit(settings, seconds))}
		/>
	</div>

	<div class="flex flex-col gap-2">
		<div class="flex items-baseline justify-between gap-2 px-1">
			<h3 class="text-sm font-bold text-cream-muted">{m.lobby_cats_title()}</h3>
			<span class="text-sm font-extrabold">{settings.categories.length}/{MAX_CATEGORIES}</span>
		</div>
		<CategoryChips refs={settings.categories} {labels} />
		<Button size="md" tone="grape" aria-haspopup="dialog" onclick={() => (pickerOpen = true)}>
			<span aria-hidden="true">✏️</span>
			{m.lobby_cats_edit()}
		</Button>
	</div>
</section>

{#if pickerOpen}
	<Sheet title={m.lobby_cats_title()} size="full" onclose={() => (pickerOpen = false)}>
		<CategoryPicker {settings} {labels} {onchange} {rng} />
		{#snippet footer()}
			<Button tone="lime" onclick={() => (pickerOpen = false)}>{m.lobby_cats_done()}</Button>
		{/snippet}
	</Sheet>
{/if}
