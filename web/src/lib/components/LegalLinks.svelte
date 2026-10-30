<script lang="ts">
	import { i18n } from '$lib/i18n.svelte';
	import { m } from '$lib/paraglide/messages';

	/**
	 * Links to the static legal pages (`static/privacy.html`, `terms.html`, `delete-account.html`, served by
	 * nginx at `/privacy`, `/terms`, `/delete-account`). They are not SPA routes: `rel="external"` makes
	 * SvelteKit leave the links alone so the browser does a real page load, in the SAME browsing context
	 * (no `target`), so that the Android app (a Trusted Web Activity) comes back with Back. The fragment
	 * selects the page's language (en, es, nl). `class` adds layout classes (Home pulls the row up under
	 * the Google button to stay on one screen).
	 */
	type Page = 'privacy' | 'terms' | 'delete-account';
	let {
		pages = ['privacy', 'terms'],
		list = false,
		class: className = ''
	}: {
		pages?: readonly Page[];
		/** Full-width rows (the legal sheet) instead of small inline links. */
		list?: boolean;
		class?: string;
	} = $props();

	const text: Record<Page, () => string> = {
		privacy: () => m.privacy_link(),
		terms: () => m.legal_terms(),
		'delete-account': () => m.legal_delete()
	};
</script>

<nav aria-label={m.legal_nav()} class={list ? 'flex flex-col gap-2' : 'contents'}>
	{#each pages as page (page)}
		<a
			href="/{page}#{i18n.locale}"
			rel="external"
			class="flex items-center {list
				? 'min-h-14 rounded-xl bg-night px-3 text-lg font-bold ring-1 ring-night-3'
				: 'h-12 justify-center rounded-2xl px-2 text-sm font-bold text-cream-muted underline underline-offset-4'} transition active:bg-night-3 {className}"
		>
			{text[page]()}
		</a>
	{/each}
</nav>
