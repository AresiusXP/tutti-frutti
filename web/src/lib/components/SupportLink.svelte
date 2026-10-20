<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { supportLink } from '$lib/support/support';

	/**
	 * The "support the game" link: a plain outbound link, no widget and no script, so the CSP and the
	 * privacy policy stay as they are. Where it goes depends on the player's region (`supportLink`).
	 * It opens in a new tab so a game in progress is never left (the room reconnects anyway), and
	 * `noreferrer` keeps the game's address from being sent along. `rel="external"`: not an SPA route
	 * (and what the SvelteKit lint rule wants on a link it cannot resolve). `class` adds layout classes.
	 */
	let { class: className = '' }: { class?: string } = $props();

	// Decided once per mount: the timezone and the language list do not change while the page is open.
	const href = supportLink();
</script>

<a
	{href}
	target="_blank"
	rel="external noopener noreferrer"
	class="flex h-12 items-center justify-center gap-1.5 rounded-2xl px-2 text-sm font-bold text-cream-muted transition active:bg-night-3 {className}"
>
	<span aria-hidden="true">☕</span>
	<span class="underline underline-offset-4">{m.support_link()}</span>
	<span class="sr-only">{m.support_new_tab()}</span>
</a>
