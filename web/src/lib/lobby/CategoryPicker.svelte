<script lang="ts">
	import { tick, untrack } from 'svelte';
	import Button from '$lib/components/Button.svelte';
	import { m } from '$lib/paraglide/messages';
	import type { Lang, Listing, Settings } from '$lib/protocol';
	import { catalog } from './catalog.svelte';
	import { groupTitle } from './groups';
	import {
		builtinIndex,
		checkCustom,
		cleanLabel,
		MAX_CATEGORIES,
		MAX_CUSTOM_LEN,
		MIN_CATEGORIES,
		normalizeLabel,
		runeCount,
		searchKey
	} from './labels';
	import { LANGS } from './letters';
	import { classicRefs, surpriseRefs, type Rng } from './presets';
	import { forgetCustom, loadRecents, rememberCustom } from './recents';
	import {
		addCustom,
		canAddCategory,
		canRemoveCategory,
		customLabels,
		hasBuiltin,
		moveCategory,
		removeCategoryAt,
		toggleBuiltin,
		withCategories
	} from './settings';

	/**
	 * The category picker (design §6.2): the selection on top (reorderable),
	 * presets, search + the catalog grouped in collapsible sections, and your
	 * own categories (validated like the server does, last 10 remembered).
	 * `settings` is what to show; every change goes out through `onchange`.
	 */
	interface Props {
		settings: Settings;
		/** Display labels of `settings.categories` (same order). */
		labels: readonly string[];
		onchange: (next: Settings) => void;
		rng?: Rng;
	}
	let { settings, labels, onchange, rng = Math.random }: Props = $props();

	const lang = $derived<Lang>(settings.lang);
	const loaded = $derived(catalog.state(lang));
	const listing = $derived(catalog.listing(lang));

	// The selected language is needed to pick; the others only to catch duplicate custom names.
	$effect(() => {
		const current = lang;
		untrack(() => {
			void catalog.load(current);
			for (const other of LANGS) if (other !== current) void catalog.load(other);
		});
	});

	const count = $derived(settings.categories.length);
	const canAdd = $derived(canAddCategory(settings));
	const canRemove = $derived(canRemoveCategory(settings));
	const customs = $derived(customLabels(settings));

	const items = $derived(
		settings.categories.map((ref, i) => ({
			key: ref.id ?? `c:${ref.custom}`,
			label: labels[i] ?? ref.custom ?? ref.id ?? '',
			custom: ref.custom !== undefined
		}))
	);

	// --- selection list -----------------------------------------------------
	const ups: Record<string, HTMLButtonElement | undefined> = {};
	const downs: Record<string, HTMLButtonElement | undefined> = {};
	const removes: Record<string, HTMLButtonElement | undefined> = {};
	let moved = $state('');

	async function move(index: number, delta: -1 | 1) {
		const item = items[index];
		const next = moveCategory(settings, index, delta);
		if (next === settings) return;
		onchange(next);
		moved = m.lobby_cat_moved({ label: item.label, pos: index + delta + 1, total: count });
		await tick();
		const preferred = delta < 0 ? ups[item.key] : downs[item.key];
		const other = delta < 0 ? downs[item.key] : ups[item.key];
		(preferred && !preferred.disabled ? preferred : other)?.focus();
	}

	async function remove(index: number) {
		const neighbour = items[index + 1] ?? items[index - 1];
		const next = removeCategoryAt(settings, index);
		if (next === settings) return;
		onchange(next);
		await tick();
		if (neighbour) removes[neighbour.key]?.focus();
	}

	// --- presets ------------------------------------------------------------
	function classic(source: Listing) {
		onchange(withCategories(settings, classicRefs(source)));
	}
	function surprise(source: Listing) {
		onchange(withCategories(settings, surpriseRefs(source, count, rng)));
	}

	// --- catalog chips ------------------------------------------------------
	let query = $state('');
	const needle = $derived(searchKey(query));
	let expanded = $state<Record<string, boolean>>({});

	const groups = $derived.by(() => {
		if (!listing) return [];
		return listing.groups
			.map((id) => {
				const all = listing.categories.filter((c) => c.group === id);
				return {
					id,
					all,
					shown: needle === '' ? all : all.filter((c) => searchKey(c.label).includes(needle))
				};
			})
			.filter((group) => group.shown.length > 0);
	});

	// --- custom categories --------------------------------------------------
	let customOpen = $state(false);
	let draft = $state('');
	let recents = $state(loadRecents());
	let recentError = $state('');
	let customInput = $state<HTMLInputElement>();

	const builtins = $derived.by(() => {
		const listings: Partial<Record<Lang, Listing>> = {};
		for (const code of LANGS) {
			const ready = catalog.listing(code);
			if (ready) listings[code] = ready;
		}
		return builtinIndex(listings, lang);
	});
	const check = $derived(checkCustom(draft, lang, builtins, customs));
	const draftMessage = $derived.by(() => {
		if (draft.trim() === '' || check.ok) return '';
		if (check.error === 'duplicate') return m.lobby_custom_error_dup({ label: check.existing });
		if (check.error === 'too_long') return m.lobby_custom_error_long({ max: check.max });
		return '';
	});
	const freshRecents = $derived(
		recents.filter(
			(label) => !customs.some((c) => normalizeLabel(c, 'en') === normalizeLabel(label, 'en'))
		)
	);

	async function openCustom(prefill = '') {
		customOpen = true;
		if (prefill) draft = Array.from(prefill).slice(0, MAX_CUSTOM_LEN).join('');
		await tick();
		customInput?.focus();
	}

	/**
	 * The duplicate check needs the built-in labels of EVERY language. A catalog that is still
	 * loading, or that failed earlier, is (re)fetched before a name is committed, so a duplicate
	 * is not let through just because one language had not arrived yet. If a language stays
	 * unreachable the name goes through and the server, which is authoritative, decides.
	 */
	async function allCatalogs() {
		await Promise.all(LANGS.map((code) => catalog.load(code)));
	}

	async function submitCustom(event: SubmitEvent) {
		event.preventDefault();
		if (!check.ok || !canAdd) return;
		await allCatalogs();
		// `check` is derived: it now sees every catalog that arrived meanwhile (and the latest draft).
		if (!check.ok || !canAdd) return;
		const label = check.label;
		onchange(addCustom(settings, label));
		recents = rememberCustom(label);
		draft = '';
		customInput?.focus();
	}

	async function addRecent(label: string) {
		await allCatalogs();
		const result = checkCustom(label, lang, builtins, customs);
		if (!result.ok) {
			recentError =
				result.error === 'duplicate' ? m.lobby_custom_error_dup({ label: result.existing }) : '';
			return;
		}
		recentError = '';
		onchange(addCustom(settings, result.label));
		recents = rememberCustom(result.label);
	}

	const chip =
		'flex min-h-12 items-center gap-1 rounded-full px-4 py-1.5 text-center text-base leading-tight font-bold transition-colors disabled:opacity-50';
</script>

<div class="flex flex-col gap-4 pb-2">
	<!-- selection -->
	<section aria-labelledby="picker-selected" class="flex flex-col gap-2">
		<div class="flex items-baseline justify-between gap-2 px-1">
			<h3 id="picker-selected" class="text-lg font-black">{m.lobby_cats_selected_title()}</h3>
			<p role="status" class="text-sm font-extrabold">
				{m.lobby_cats_count({ count, max: MAX_CATEGORIES })}
			</p>
		</div>
		<p class="px-1 text-sm text-cream-muted">
			{#if count >= MAX_CATEGORIES}
				{m.lobby_cats_at_max({ max: MAX_CATEGORIES })}
			{:else if count <= MIN_CATEGORIES}
				{m.lobby_cats_at_min({ min: MIN_CATEGORIES })}
			{:else}
				{m.lobby_cats_hint({ min: MIN_CATEGORIES, max: MAX_CATEGORIES })}
			{/if}
		</p>
		<p class="sr-only" role="status" aria-live="polite">{moved}</p>
		<ol class="flex flex-col gap-1.5">
			{#each items as item, i (item.key)}
				<li class="flex min-h-12 items-center gap-1 rounded-2xl bg-night pl-3 ring-1 ring-night-3">
					<span class="w-5 shrink-0 text-sm font-extrabold text-cream-muted" aria-hidden="true"
						>{i + 1}</span
					>
					<span class="min-w-0 flex-1 py-1 font-bold">
						{item.label}
						{#if item.custom}
							<span role="img" aria-label={m.lobby_cat_custom_mark()}>✏️</span>
						{/if}
					</span>
					<button
						type="button"
						bind:this={ups[item.key]}
						aria-label={m.lobby_cat_up({ label: item.label })}
						disabled={i === 0}
						onclick={() => move(i, -1)}
						class="flex size-12 shrink-0 items-center justify-center rounded-xl text-lg disabled:opacity-30"
					>
						<span aria-hidden="true">▲</span>
					</button>
					<button
						type="button"
						bind:this={downs[item.key]}
						aria-label={m.lobby_cat_down({ label: item.label })}
						disabled={i === items.length - 1}
						onclick={() => move(i, 1)}
						class="flex size-12 shrink-0 items-center justify-center rounded-xl text-lg disabled:opacity-30"
					>
						<span aria-hidden="true">▼</span>
					</button>
					<button
						type="button"
						bind:this={removes[item.key]}
						aria-label={m.lobby_cat_remove({ label: item.label })}
						disabled={!canRemove}
						onclick={() => remove(i)}
						class="flex size-12 shrink-0 items-center justify-center rounded-xl text-lg text-strawberry disabled:opacity-30"
					>
						<span aria-hidden="true">✕</span>
					</button>
				</li>
			{/each}
		</ol>
	</section>

	<!-- presets (need the catalog; when it failed, the retry sits here where it is seen) -->
	{#if loaded?.status === 'error'}
		<div
			role="alert"
			class="flex flex-col items-center gap-2 rounded-2xl bg-night p-3 text-center ring-1 ring-strawberry"
		>
			<p class="font-bold">{m.lobby_cats_error()}</p>
			<Button size="md" tone="strawberry" block={false} onclick={() => catalog.load(lang)}>
				{m.lobby_retry()}
			</Button>
		</div>
	{:else}
		<div class="grid grid-cols-2 gap-2">
			<Button
				size="md"
				tone="plain"
				disabled={!listing}
				onclick={() => listing && classic(listing)}
				class="px-2! whitespace-nowrap"
			>
				{m.lobby_preset_classic()}
			</Button>
			<Button
				size="md"
				tone="plain"
				disabled={!listing}
				onclick={() => listing && surprise(listing)}
				class="px-2! whitespace-nowrap"
			>
				<span aria-hidden="true">🎲</span>
				{m.lobby_preset_surprise()}
			</Button>
		</div>
	{/if}

	<!-- catalog -->
	{#if loaded?.status !== 'error'}
		<section aria-labelledby="picker-catalog" class="flex flex-col gap-2">
			<h3 id="picker-catalog" class="px-1 text-lg font-black">{m.lobby_cats_catalog_title()}</h3>
			<div class="flex flex-col gap-1">
				<label for="picker-search" class="px-1 text-sm font-bold text-cream-muted">
					{m.lobby_search_label()}
				</label>
				<input
					id="picker-search"
					name="category-search"
					type="search"
					bind:value={query}
					placeholder={m.lobby_search_placeholder()}
					autocomplete="off"
					autocapitalize="off"
					spellcheck="false"
					enterkeyhint="search"
					class="h-12 w-full rounded-xl bg-night px-4 text-lg font-bold ring-2 ring-night-3 placeholder:font-normal placeholder:text-cream-muted/70 focus:ring-banana focus:outline-none"
				/>
			</div>

			{#if !loaded || loaded.status === 'loading'}
				<div role="status" aria-busy="true" class="flex flex-col gap-2">
					<span class="sr-only">{m.lobby_cats_loading()}</span>
					{#each [0, 1, 2, 3, 4] as n (n)}
						<div class="h-12 animate-pulse rounded-2xl bg-night-3/60" aria-hidden="true"></div>
					{/each}
				</div>
			{:else if groups.length === 0}
				<div
					class="flex flex-col items-center gap-2 rounded-2xl bg-night p-4 text-center ring-1 ring-night-3"
				>
					<p class="font-bold">{m.lobby_search_empty({ query: query.trim() })}</p>
					<Button
						size="md"
						tone="plain"
						block={false}
						onclick={() => openCustom(cleanLabel(query))}
					>
						{m.lobby_search_add_custom()}
					</Button>
				</div>
			{:else}
				<ul class="flex flex-col gap-1.5">
					{#each groups as group (group.id)}
						{@const open = needle !== '' || expanded[group.id] === true}
						{@const picked = group.all.filter((c) => hasBuiltin(settings, c.id)).length}
						<li class="rounded-2xl bg-night ring-1 ring-night-3">
							<button
								type="button"
								aria-expanded={open}
								aria-controls="group-{group.id}"
								onclick={() => (expanded[group.id] = !open)}
								class="flex min-h-12 w-full items-center gap-2 rounded-2xl px-3 text-left"
							>
								<span class="flex-1 font-extrabold">{groupTitle(group.id)}</span>
								<span class="text-sm text-cream-muted">
									{m.lobby_group_count({ selected: picked, total: group.all.length })}
								</span>
								<span class="text-cream-muted" aria-hidden="true">{open ? '▾' : '▸'}</span>
							</button>
							{#if open}
								<ul id="group-{group.id}" class="flex flex-wrap gap-1.5 px-2 pb-2">
									{#each group.shown as category (category.id)}
										{@const selected = hasBuiltin(settings, category.id)}
										<li>
											<button
												type="button"
												aria-pressed={selected}
												disabled={selected ? !canRemove : !canAdd}
												onclick={() => onchange(toggleBuiltin(settings, category.id))}
												class="{chip} {selected ? 'bg-banana text-night' : 'bg-night-3 text-cream'}"
											>
												{#if selected}<span aria-hidden="true">✓</span>{/if}
												{category.label}
											</button>
										</li>
									{/each}
								</ul>
							{/if}
						</li>
					{/each}
				</ul>
			{/if}
		</section>
	{/if}

	<!-- custom -->
	<section aria-labelledby="picker-custom" class="flex flex-col gap-2">
		<h3 id="picker-custom" class="px-1 text-lg font-black">{m.lobby_custom_title()}</h3>
		{#if !customOpen}
			<Button
				size="md"
				tone="plain"
				aria-expanded="false"
				aria-controls="custom-form"
				onclick={() => openCustom()}
			>
				<span aria-hidden="true">＋</span>
				{m.lobby_custom_add()}
			</Button>
		{:else}
			<form id="custom-form" onsubmit={submitCustom} class="flex flex-col gap-1" novalidate>
				<div class="flex items-baseline justify-between gap-2 px-1">
					<label for="custom-input" class="text-sm font-bold text-cream-muted">
						{m.lobby_custom_label()}
					</label>
					<span id="custom-count" class="text-xs text-cream-muted">
						{m.lobby_custom_counter({ count: runeCount(draft), max: MAX_CUSTOM_LEN })}
					</span>
				</div>
				<p id="custom-message" role="alert" class="min-h-5 px-1 text-sm font-bold">
					{#if draftMessage}
						<span class="text-strawberry">{draftMessage}</span>
					{:else if !canAdd}
						<span class="text-cream-muted">{m.lobby_custom_full({ max: MAX_CATEGORIES })}</span>
					{/if}
				</p>
				<div class="flex gap-2">
					<input
						id="custom-input"
						name="custom-category"
						bind:this={customInput}
						bind:value={draft}
						maxlength={MAX_CUSTOM_LEN}
						placeholder={m.lobby_custom_placeholder()}
						autocomplete="off"
						autocapitalize="sentences"
						spellcheck="false"
						enterkeyhint="done"
						aria-invalid={draftMessage !== ''}
						aria-describedby="custom-count custom-message"
						class="h-12 min-w-0 flex-1 rounded-xl bg-night px-4 text-lg font-bold ring-2 placeholder:font-normal placeholder:text-cream-muted/70 focus:ring-banana focus:outline-none {draftMessage
							? 'ring-strawberry'
							: 'ring-night-3'}"
					/>
					<Button
						type="submit"
						size="md"
						tone="banana"
						block={false}
						disabled={!check.ok || !canAdd}
					>
						{m.lobby_custom_submit()}
					</Button>
				</div>
			</form>
		{/if}

		{#if freshRecents.length > 0}
			<div role="group" aria-labelledby="recents-title" class="flex flex-col gap-1">
				<p id="recents-title" class="px-1 text-sm font-bold text-cream-muted">
					{m.lobby_recents_title()}
				</p>
				<ul class="flex flex-wrap gap-1.5">
					{#each freshRecents as label (label)}
						<li class="flex rounded-full bg-night-3 pl-1 ring-1 ring-night-3">
							<button
								type="button"
								aria-label={m.lobby_recent_add({ label })}
								disabled={!canAdd}
								onclick={() => addRecent(label)}
								class="flex min-h-12 items-center gap-1 rounded-l-full pr-1 pl-3 text-base font-bold disabled:opacity-50"
							>
								<span aria-hidden="true">✏️ {label}</span>
							</button>
							<button
								type="button"
								aria-label={m.lobby_recent_forget({ label })}
								onclick={() => (recents = forgetCustom(label))}
								class="flex size-12 items-center justify-center rounded-r-full text-cream-muted hover:text-strawberry"
							>
								<span aria-hidden="true">✕</span>
							</button>
						</li>
					{/each}
				</ul>
				{#if recentError}
					<p role="alert" class="px-1 text-sm font-bold text-strawberry">{recentError}</p>
				{/if}
			</div>
		{/if}
	</section>
</div>
