<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import LegalSheet from './LegalSheet.svelte';

	/** The small round button of Home's header that opens the legal / safety sheet. */
	let open = $state(false);
</script>

<button
	type="button"
	aria-haspopup="dialog"
	aria-label={m.legal_sheet_aria()}
	data-testid="legal-button"
	onclick={() => (open = true)}
	class="flex size-12 shrink-0 items-center justify-center rounded-full bg-night-2 text-cream-muted ring-1 ring-night-3 transition active:bg-night-3"
>
	<svg
		aria-hidden="true"
		viewBox="0 0 24 24"
		class="size-6"
		fill="none"
		stroke="currentColor"
		stroke-width="2"
		stroke-linecap="round"
		stroke-linejoin="round"
	>
		<path d="M12 3 5 6v5c0 4.5 2.9 8.3 7 10 4.1-1.7 7-5.5 7-10V6z" />
		<path d="M12 11v5M12 8h.01" />
	</svg>
</button>

{#if open}
	<LegalSheet onclose={() => (open = false)} />
{/if}
