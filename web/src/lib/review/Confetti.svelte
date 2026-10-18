<script lang="ts">
	/** A few confetti pieces that fall once. CSS only; hidden with reduced motion. */
	const colors = ['#ffd23f', '#ff4f81', '#7ddc3b', '#9b6cff', '#4cc9f0', '#ff8a1f'];
	const pieces = Array.from({ length: 18 }, (_, i) => ({
		left: (i * 37 + 11) % 100,
		delay: (i % 6) * 220,
		duration: 2400 + ((i * 53) % 5) * 260,
		color: colors[i % colors.length],
		spin: i % 2 === 0 ? 1 : -1
	}));
</script>

<div class="confetti pointer-events-none fixed inset-0 overflow-hidden" aria-hidden="true">
	{#each pieces as piece, i (i)}
		<span
			style:left="{piece.left}%"
			style:background-color={piece.color}
			style:animation-delay="{piece.delay}ms"
			style:animation-duration="{piece.duration}ms"
			style:--spin="{piece.spin * 540}deg"
		></span>
	{/each}
</div>

<style>
	.confetti span {
		position: absolute;
		top: -16px;
		width: 10px;
		height: 16px;
		border-radius: 2px;
		opacity: 0;
		animation: confetti-fall 3000ms ease-in 1 both;
	}
	@keyframes confetti-fall {
		0% {
			opacity: 1;
			transform: translateY(0) rotate(0deg);
		}
		85% {
			opacity: 1;
		}
		100% {
			opacity: 0;
			transform: translateY(105dvh) rotate(var(--spin));
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.confetti {
			display: none;
		}
	}
</style>
