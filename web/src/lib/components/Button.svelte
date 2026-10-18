<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLButtonAttributes } from 'svelte/elements';

	/**
	 * The chunky "pop" button of the app (>= 48 px touch target, 3D press).
	 * `tone` picks the fruit colour; `size="lg"` is the 56 px primary action.
	 */
	type Tone = 'banana' | 'strawberry' | 'lime' | 'grape' | 'plain';
	interface Props extends Omit<HTMLButtonAttributes, 'class' | 'children'> {
		tone?: Tone;
		size?: 'md' | 'lg';
		block?: boolean;
		class?: string;
		children: Snippet;
	}
	let {
		tone = 'banana',
		size = 'lg',
		block = true,
		type = 'button',
		class: extra = '',
		children,
		...rest
	}: Props = $props();

	const tones: Record<Tone, string> = {
		banana: 'bg-banana text-night',
		strawberry: 'bg-strawberry text-night',
		lime: 'bg-lime text-night',
		grape: 'bg-grape text-night',
		plain: 'bg-night-3 text-cream'
	};
</script>

<button
	{type}
	{...rest}
	class="{tones[tone]} {size === 'lg' ? 'h-14 px-6 text-xl' : 'h-12 px-4 text-base'} {block
		? 'w-full'
		: ''} rounded-2xl font-black shadow-pop transition active:translate-y-1 active:shadow-none disabled:bg-night-3 disabled:text-cream-muted disabled:shadow-none {extra}"
>
	{@render children()}
</button>
