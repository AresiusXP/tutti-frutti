<script lang="ts">
	import { AVATAR_COLORS, AVATAR_EMOJIS, type Avatar } from '$lib/avatar';
	import { m } from '$lib/paraglide/messages';

	let {
		avatar,
		onchange
	}: {
		avatar: Avatar;
		onchange: (next: Partial<Avatar>) => void;
	} = $props();

	const colorLabels = {
		banana: () => m.color_banana(),
		orange: () => m.color_orange(),
		strawberry: () => m.color_strawberry(),
		lime: () => m.color_lime(),
		grape: () => m.color_grape(),
		blueberry: () => m.color_blueberry()
	} as const;

	// Six 48px targets plus gaps need 308px; below that (very narrow phones) wrap to 3 columns.
	const row = 'grid grid-cols-3 gap-1 min-[22rem]:grid-cols-6';
</script>

<div class="flex flex-col gap-1">
	<div role="group" aria-label={m.avatar_emoji_group()} class={row}>
		{#each AVATAR_EMOJIS as emoji (emoji)}
			<button
				type="button"
				aria-pressed={avatar.emoji === emoji}
				onclick={() => onchange({ emoji })}
				class="flex h-12 min-w-12 items-center justify-center rounded-2xl text-2xl transition-transform active:scale-95 {avatar.emoji ===
				emoji
					? 'bg-night-3 ring-3 ring-banana'
					: 'bg-night ring-1 ring-night-3 hover:bg-night-3'}"
			>
				{emoji}
			</button>
		{/each}
	</div>

	<div role="group" aria-label={m.avatar_color_group()} class={row}>
		{#each AVATAR_COLORS as color (color.id)}
			<button
				type="button"
				aria-pressed={avatar.color === color.id}
				aria-label={colorLabels[color.id]()}
				onclick={() => onchange({ color: color.id })}
				class="flex h-12 min-w-12 items-center justify-center rounded-2xl bg-night transition-transform active:scale-95 {avatar.color ===
				color.id
					? 'ring-3 ring-cream'
					: 'ring-1 ring-night-3'}"
			>
				<span
					class="size-7 rounded-full shadow-pop-sm"
					style:background-color={color.hex}
					aria-hidden="true"
				></span>
			</button>
		{/each}
	</div>
</div>
