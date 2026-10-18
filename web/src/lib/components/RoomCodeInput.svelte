<script lang="ts">
	import { tick } from 'svelte';
	import { m } from '$lib/paraglide/messages';
	import {
		ROOM_CODE_LENGTH,
		backspaceAt,
		isCompleteRoomCode,
		normalizePastedCode,
		typeIntoBox,
		type CodeEdit
	} from '$lib/roomCode';

	let {
		value = $bindable(''),
		disabled = false,
		describedby,
		oncomplete,
		id = 'room-code'
	}: {
		value?: string;
		disabled?: boolean;
		/** Prefix of the boxes' `id` / `name` (`room-code-1` … `room-code-4`): unique per page, stable. */
		id?: string;
		/** Id of an element describing the code format (e.g. the "no vowels" hint). */
		describedby?: string;
		/** Called when the user finishes typing/pasting a full code, or presses Enter on a full code. */
		oncomplete?: (code: string) => void;
	} = $props();

	let boxes: HTMLInputElement[] = $state([]);
	const indexes = Array.from({ length: ROOM_CODE_LENGTH }, (_, i) => i);

	async function commit(edit: CodeEdit) {
		value = edit.value;
		await tick();
		boxes.forEach((box, i) => (box.value = value[i] ?? ''));
		boxes[edit.focus]?.focus();
		boxes[edit.focus]?.select();
		if (isCompleteRoomCode(value)) oncomplete?.(value);
	}

	function onInput(index: number, event: Event) {
		const el = event.currentTarget as HTMLInputElement;
		if (el.value === '') {
			void commit(backspaceAt(value, index));
		} else {
			void commit(typeIntoBox(value, index, el.value));
		}
	}

	function onKeydown(index: number, event: KeyboardEvent) {
		switch (event.key) {
			case 'Backspace':
				event.preventDefault();
				void commit(backspaceAt(value, index));
				break;
			case 'ArrowLeft':
				event.preventDefault();
				boxes[Math.max(index - 1, 0)]?.focus();
				break;
			case 'ArrowRight':
				event.preventDefault();
				boxes[Math.min(index + 1, ROOM_CODE_LENGTH - 1)]?.focus();
				break;
			case 'Enter':
				if (isCompleteRoomCode(value)) oncomplete?.(value);
				break;
		}
	}

	function onPaste(event: ClipboardEvent) {
		event.preventDefault();
		const code = normalizePastedCode(event.clipboardData?.getData('text') ?? '');
		// Text that is not a code is ignored: what was already typed stays untouched.
		if (code) void commit({ value: code, focus: Math.min(code.length, ROOM_CODE_LENGTH - 1) });
	}
</script>

<div
	class="flex justify-center gap-3"
	role="group"
	aria-label={m.code_label()}
	aria-describedby={describedby}
	data-testid="room-code"
>
	{#each indexes as i (i)}
		<input
			bind:this={boxes[i]}
			id="{id}-{i + 1}"
			name="{id}-{i + 1}"
			type="text"
			inputmode="text"
			autocapitalize="characters"
			autocomplete="off"
			autocorrect="off"
			spellcheck="false"
			enterkeyhint="go"
			{disabled}
			aria-label={m.code_char_label({ n: i + 1, total: ROOM_CODE_LENGTH })}
			value={value[i] ?? ''}
			onfocus={(e) => e.currentTarget.select()}
			oninput={(e) => onInput(i, e)}
			onkeydown={(e) => onKeydown(i, e)}
			onpaste={onPaste}
			class="h-14 min-h-12 w-14 min-w-12 rounded-2xl bg-night-2 text-center text-3xl font-black text-banana uppercase caret-transparent ring-2 ring-night-3 transition-shadow focus:ring-4 focus:ring-banana focus:outline-none disabled:opacity-50"
		/>
	{/each}
</div>
