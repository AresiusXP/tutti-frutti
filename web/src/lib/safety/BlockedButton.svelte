<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import BlockedSheet from './BlockedSheet.svelte';

	/** The "Blocked players" entry (Home, My stats): a text button that opens the list sheet. */
	let { class: className = '' }: { class?: string } = $props();
	let open = $state(false);
</script>

<button
	type="button"
	aria-haspopup="dialog"
	onclick={() => (open = true)}
	class="flex h-12 items-center justify-center rounded-2xl px-2 text-sm font-bold text-cream-muted underline underline-offset-4 transition active:bg-night-3 {className}"
>
	{m.blocked_link()}
</button>

{#if open}
	<BlockedSheet onclose={() => (open = false)} />
{/if}
