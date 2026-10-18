<script lang="ts">
	import { colorHex } from '$lib/avatar';

	/** Accepts the local profile avatar and the wire `Avatar` (colour is a palette id, anything else falls back). */
	let { avatar, size = 56 }: { avatar: { emoji: string; color: string }; size?: number } = $props();
</script>

<span
	class="inline-flex shrink-0 items-center justify-center rounded-full shadow-pop-sm"
	style:width="{size}px"
	style:height="{size}px"
	style:background-color={colorHex(avatar.color)}
	style:font-size="{Math.round(size * 0.55)}px"
	aria-hidden="true"
>
	{avatar.emoji}
</span>
