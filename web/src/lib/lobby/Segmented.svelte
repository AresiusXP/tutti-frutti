<script lang="ts" generics="T extends string | number">
	/**
	 * A row of exclusive toggle buttons (same pattern as the language switch):
	 * `role="group"` + `aria-pressed`, every segment >= 48 px tall.
	 */
	interface Option {
		value: T;
		label: string;
		/** Spoken name when the visible label is terse ("Off", "60 s"). */
		ariaLabel?: string;
	}
	interface Props {
		label: string;
		options: readonly Option[];
		value: T;
		onchange: (value: T) => void;
	}
	let { label, options, value, onchange }: Props = $props();
</script>

<div
	role="group"
	aria-label={label}
	class="flex gap-1 rounded-2xl bg-night p-1 ring-2 ring-night-3"
>
	{#each options as option (option.value)}
		<button
			type="button"
			aria-pressed={value === option.value}
			aria-label={option.ariaLabel}
			onclick={() => onchange(option.value)}
			class="min-h-12 min-w-0 flex-1 rounded-xl px-2 text-base font-extrabold transition-colors {value ===
			option.value
				? 'bg-banana text-night'
				: 'text-cream-muted hover:text-cream'}"
		>
			{option.label}
		</button>
	{/each}
</div>
