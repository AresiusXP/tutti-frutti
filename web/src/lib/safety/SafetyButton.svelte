<script lang="ts">
	import { useSafety, type SafetyRequest } from './context';

	/**
	 * A flag button (48 px hit area) that opens the report / block sheet for `request`. It renders
	 * nothing without a host, and nothing for the viewer's own seat or an automated one.
	 */
	let {
		request,
		label,
		text = false,
		class: extra = ''
	}: {
		request: SafetyRequest;
		/** Accessible name: says WHO or WHAT it is about (the flag itself is only an icon). */
		label: string;
		/** Show `label` as the button's visible text (then it is also its name) instead of an icon. */
		text?: boolean;
		class?: string;
	} = $props();

	const safety = useSafety();
	const shown = $derived.by(() => {
		if (!safety) return false;
		if (request.start === 'person') return safety.canTarget(request.person.id);
		return true;
	});
</script>

{#if shown && safety}
	<button
		type="button"
		aria-haspopup="dialog"
		aria-label={text ? undefined : label}
		data-testid="safety-button"
		onclick={() => safety.open(request)}
		class="flex shrink-0 items-center justify-center rounded-xl text-cream-muted transition hover:text-cream active:bg-night-3 {text
			? 'h-12 gap-2 px-4 text-sm font-bold underline underline-offset-4'
			: 'size-12'} {extra}"
	>
		<svg aria-hidden="true" viewBox="0 0 24 24" class="size-5 shrink-0" fill="currentColor">
			<path d="M6 3h2v18H6zM9 4h10l-2.5 4L19 12H9z" />
		</svg>
		{#if text}{label}{/if}
	</button>
{/if}
