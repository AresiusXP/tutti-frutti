<script lang="ts">
	import LegalLinks from '$lib/components/LegalLinks.svelte';
	import Sheet from '$lib/lobby/Sheet.svelte';
	import { m } from '$lib/paraglide/messages';
	import BlockedSheet from './BlockedSheet.svelte';

	/**
	 * "Terms, safety and legal": the legal pages (real page loads, same browsing context) and the block
	 * list. Opened from the (i) button of Home, which has no room for more links in its footer row.
	 */
	let { onclose }: { onclose: () => void } = $props();
	let blockedOpen = $state(false);
</script>

<Sheet title={m.legal_sheet_title()} {onclose}>
	<div class="flex flex-col gap-2 pb-2" data-testid="legal-sheet">
		<p class="text-cream-muted">{m.legal_sheet_terms_hint()}</p>
		<LegalLinks list pages={['terms', 'privacy', 'delete-account']} />
		<button
			type="button"
			aria-haspopup="dialog"
			onclick={() => (blockedOpen = true)}
			class="flex min-h-14 items-center rounded-xl bg-night px-3 text-left text-lg font-bold ring-1 ring-night-3 transition active:bg-night-3"
		>
			{m.blocked_link()}
		</button>
	</div>
</Sheet>

{#if blockedOpen}
	<BlockedSheet onclose={() => (blockedOpen = false)} />
{/if}
