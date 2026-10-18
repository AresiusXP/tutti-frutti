<script lang="ts">
	/** One lifetime number: icon, big value, label and an optional derived caption. */
	let {
		icon,
		label,
		value,
		hint,
		tone = 'text-banana',
		wide = false
	}: {
		icon: string;
		label: string;
		value: string;
		hint?: string;
		/** Tailwind text colour class of the value (a fruit colour: all pass contrast on the card). */
		tone?: string;
		wide?: boolean;
	} = $props();
</script>

<div
	class="flex flex-col gap-0.5 rounded-3xl bg-night-2/80 p-3 ring-1 ring-night-3 {wide
		? 'col-span-2'
		: ''}"
>
	<div class="flex items-center gap-1.5 text-sm font-bold text-cream-muted">
		<span aria-hidden="true">{icon}</span>
		<span>{label}</span>
	</div>
	<p class="text-4xl leading-tight font-black tabular-nums {tone}">{value}</p>
	{#if hint}<p class="text-sm text-cream-muted">{hint}</p>{/if}
</div>
