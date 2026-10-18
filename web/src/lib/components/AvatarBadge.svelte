<script lang="ts">
	import { colorHex, type Avatar } from '$lib/avatar';

	let { avatar, size = 56 }: { avatar: Avatar; size?: number } = $props();
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
