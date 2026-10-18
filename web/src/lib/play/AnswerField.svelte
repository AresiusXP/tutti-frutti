<script lang="ts">
	import { m } from '$lib/paraglide/messages';
	import { MAX_ANSWER_LEN, type FieldStatus } from './answer';

	interface Props {
		id: string;
		label: string;
		value: string;
		status: FieldStatus;
		letter: string;
		disabled?: boolean;
		/** The last field shows "done" on the keyboard instead of "next". */
		last?: boolean;
		input?: HTMLInputElement | null;
		oninput: (text: string) => void;
		onblur: () => void;
		onenter: () => void;
	}
	let {
		id,
		label,
		value,
		status,
		letter,
		disabled = false,
		last = false,
		input = $bindable(),
		oninput,
		onblur,
		onenter
	}: Props = $props();

	function onkeydown(event: KeyboardEvent) {
		if (event.key !== 'Enter' || event.isComposing) return;
		event.preventDefault();
		onenter();
	}
</script>

<div class="flex scroll-mt-44 scroll-mb-36 flex-col gap-1">
	<div class="flex flex-wrap items-baseline justify-between gap-x-3 px-1">
		<label for={id} class="text-base font-extrabold text-cream">{label}</label>
		{#if status === 'wrong_letter'}
			<span id="{id}-status" class="text-sm font-bold text-strawberry">
				<span aria-hidden="true">⚠</span>
				{m.play_field_wrong_letter({ letter })}
			</span>
		{:else if status === 'valid'}
			<span id="{id}-status" class="text-base font-black text-lime">
				<span aria-hidden="true">✓</span>
				<span class="sr-only">{m.play_field_valid()}</span>
			</span>
		{/if}
	</div>
	<input
		{id}
		bind:this={input}
		{value}
		{disabled}
		oninput={(event) => oninput(event.currentTarget.value)}
		{onblur}
		{onkeydown}
		type="text"
		autocomplete="off"
		autocorrect="off"
		autocapitalize="words"
		spellcheck="false"
		enterkeyhint={last ? 'done' : 'next'}
		maxlength={MAX_ANSWER_LEN}
		aria-invalid={status === 'wrong_letter' ? 'true' : undefined}
		aria-describedby={status === 'empty' ? undefined : `${id}-status`}
		class="h-14 w-full rounded-2xl border-2 bg-night-2 px-4 text-xl font-semibold text-cream placeholder:text-cream-muted disabled:opacity-60 {status ===
		'valid'
			? 'border-lime'
			: status === 'wrong_letter'
				? 'border-strawberry'
				: 'border-night-3'}"
	/>
</div>
