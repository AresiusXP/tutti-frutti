<script lang="ts">
	import { account } from '$lib/account';
	import { m } from '$lib/paraglide/messages';

	/**
	 * "Sign in with Google": a real full-page navigation (`account.login`). `returnTo` is `/` or
	 * `/<ROOMCODE>`. `row` is the quiet full-width action of Home, `compact` a pill that fits next to
	 * the language switch (invite gate) and `primary` the main action of a screen (stats prompt).
	 */
	let {
		returnTo = '/',
		variant = 'row'
	}: { returnTo?: string; variant?: 'row' | 'compact' | 'primary' } = $props();

	const styles = {
		row: 'h-12 w-full rounded-2xl px-4 font-bold text-cream ring-1 ring-night-3 active:bg-night-3',
		compact:
			'min-h-12 rounded-full bg-night-2 px-4 text-base font-bold text-cream ring-1 ring-night-3 active:bg-night-3',
		primary:
			'h-14 w-full rounded-2xl bg-cream px-4 text-lg font-black whitespace-nowrap text-night shadow-pop active:translate-y-1 active:shadow-none'
	};
</script>

<button
	type="button"
	aria-label={variant === 'compact' ? m.account_sign_in_google() : undefined}
	onclick={() => account.login(returnTo)}
	class="flex items-center justify-center gap-2 transition {styles[variant]}"
>
	<!-- a neutral "G" drawn with a stroke: no external image, font or brand artwork -->
	<svg
		viewBox="0 0 24 24"
		class="size-5 shrink-0"
		fill="none"
		stroke="currentColor"
		stroke-width="3"
		stroke-linecap="round"
		stroke-linejoin="round"
		aria-hidden="true"
	>
		<path d="M17.7 6.3A8 8 0 1 0 20 12h-8" />
	</svg>
	<span>{variant === 'compact' ? m.account_sign_in() : m.account_sign_in_google()}</span>
</button>
