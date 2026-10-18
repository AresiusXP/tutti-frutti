<script lang="ts" generics="T extends string | number">
	/**
	 * A row of exclusive toggle buttons (same pattern as the language switch):
	 * `role="group"` + `aria-pressed`, every segment >= 48 px tall.
	 *
	 * A segment is named by its visible text and nothing else, on purpose. A terse label ("Off",
	 * "60 s") that a different `aria-label` spells out ("No time limit", "60 seconds") breaks WCAG
	 * 2.5.3 (label in name): a voice-control user says what they SEE, "click 60 s". The group's own
	 * `label` ("Time per round") already gives the context a screen reader needs.
	 */
	interface Option {
		value: T;
		label: string;
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
			onclick={() => onchange(option.value)}
			class="min-h-12 min-w-0 flex-1 rounded-xl px-1 text-[15px] font-extrabold transition-colors min-[380px]:px-2 min-[380px]:text-base {value ===
			option.value
				? 'bg-banana text-night'
				: 'text-cream-muted hover:text-cream'}"
		>
			{option.label}
		</button>
	{/each}
</div>
