<script lang="ts">
	import { i18n } from '$lib/i18n.svelte';
	import { m } from '$lib/paraglide/messages';

	/**
	 * Link to the privacy policy. The policy is a static page of its own (static/privacy.html,
	 * served at /privacy by nginx), not an SPA route: `rel="external"` makes SvelteKit leave the link
	 * alone so the browser does a real page load. The fragment selects the page's language (en, es, nl).
	 * `class` adds layout classes (the Home screen pulls it up under the Google row to stay on one screen).
	 */
	let { class: className = '' }: { class?: string } = $props();
</script>

<a
	href="/privacy#{i18n.locale}"
	rel="external"
	class="flex h-12 items-center justify-center rounded-2xl px-2 text-sm font-bold text-cream-muted underline underline-offset-4 transition active:bg-night-3 {className}"
>
	{m.privacy_link()}
</a>
