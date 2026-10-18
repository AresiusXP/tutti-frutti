<script lang="ts">
	import { BRAND } from '$lib/brand';

	const words = BRAND.split(' ');
	const colors = ['text-banana', 'text-strawberry', 'text-lime', 'text-tangerine'];
</script>

<div class="flex flex-wrap items-center justify-center gap-x-2 gap-y-1">
	<img
		src="/logo-mark.svg"
		alt=""
		width="62"
		height="50"
		class="h-12 w-auto animate-bounce-in drop-shadow-[0_4px_0_rgb(0_0_0/0.3)] tall:h-16"
	/>
	<h1
		class="flex gap-x-2 text-4xl leading-none font-black tracking-tight whitespace-nowrap tall:text-5xl"
	>
		{#each words as word, i (i)}
			<span
				class="{colors[i % colors.length]} inline-block drop-shadow-[0_3px_0_rgb(0_0_0/0.35)] {i %
					2 ===
				0
					? '-rotate-3'
					: 'rotate-2'}">{word}</span
			>
		{/each}
	</h1>
</div>
