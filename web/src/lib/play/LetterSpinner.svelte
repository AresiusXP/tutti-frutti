<script lang="ts">
	/** Decorative "?" that spins while the letter is being chosen. It never knows the letter. */
	let { size = 160 }: { size?: number } = $props();
</script>

<div
	class="relative grid place-items-center"
	style:width="{size}px"
	style:height="{size}px"
	aria-hidden="true"
>
	<div class="absolute inset-0 rounded-full ring"></div>
	<div
		class="coin grid place-items-center rounded-full bg-banana font-black text-night shadow-pop"
		style:width="{size * 0.78}px"
		style:height="{size * 0.78}px"
		style:font-size="{size * 0.5}px"
	>
		<span class="leading-none">?</span>
	</div>
</div>

<style>
	.ring {
		background: conic-gradient(
			from 0deg,
			var(--color-strawberry),
			var(--color-tangerine),
			var(--color-banana),
			var(--color-lime),
			var(--color-blueberry),
			var(--color-grape),
			var(--color-strawberry)
		);
		mask: radial-gradient(farthest-side, transparent calc(100% - 7px), #000 calc(100% - 6px));
		animation: ring-spin 3s linear infinite;
	}
	.coin {
		animation: coin-flip 1.4s linear infinite;
	}
	@keyframes ring-spin {
		to {
			transform: rotate(360deg);
		}
	}
	@keyframes coin-flip {
		to {
			transform: rotateY(360deg);
		}
	}
</style>
