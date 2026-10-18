<script lang="ts">
	import '../app.css';
	import OfflineBanner from '$lib/components/OfflineBanner.svelte';
	import Toasts from '$lib/components/Toasts.svelte';
	import UpdatePrompt from '$lib/components/UpdatePrompt.svelte';
	import { BRAND } from '$lib/brand';
	import { i18n } from '$lib/i18n.svelte';
	import { m } from '$lib/paraglide/messages';

	let { children } = $props();

	$effect(() => {
		document.documentElement.lang = i18n.locale;
	});
</script>

<svelte:head>
	<title>{BRAND}</title>
	<meta name="description" content={m.meta_description({ brand: BRAND })} />
</svelte:head>

<div class="sticky top-0 z-50 flex flex-col">
	<OfflineBanner />
	<UpdatePrompt />
</div>

{@render children()}

<Toasts />
