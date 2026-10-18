<script lang="ts">
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import type { Avatar } from '$lib/avatar';
	import { m } from '$lib/paraglide/messages';

	interface Props {
		/** Who called Basta; absent when the time limit expired. */
		avatar?: Avatar;
		name?: string;
		timeUp?: boolean;
		onclose: () => void;
	}
	let { avatar, name = '', timeUp = false, onclose }: Props = $props();
</script>

<!-- Purely visual: the same text is announced by the assertive region of the screen. -->
<div
	class="fixed inset-0 z-50 grid place-items-center bg-strawberry px-6 text-night"
	onclick={onclose}
	aria-hidden="true"
	data-testid="basta-splash"
>
	<div class="splash flex max-w-sm flex-col items-center gap-4 text-center">
		{#if timeUp}
			<span class="text-8xl">⏰</span>
			<p class="text-5xl leading-tight font-black">{m.play_time_up()}</p>
		{:else}
			{#if avatar}<span class="rounded-full ring-4 ring-night"
					><AvatarBadge {avatar} size={112} /></span
				>{/if}
			<p class="text-3xl font-black break-words">{name}</p>
			<p class="text-5xl leading-tight font-black">{m.basta_overlay()}</p>
		{/if}
	</div>
</div>

<style>
	.splash {
		animation: splash-in 0.35s cubic-bezier(0.34, 1.56, 0.64, 1) both;
	}
	@keyframes splash-in {
		from {
			opacity: 0;
			transform: scale(0.6) rotate(-4deg);
		}
		to {
			opacity: 1;
			transform: none;
		}
	}
</style>
