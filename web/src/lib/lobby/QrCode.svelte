<script lang="ts">
	import { m } from '$lib/paraglide/messages';

	/**
	 * A QR code as an inline SVG: dark modules on a white card (strong contrast,
	 * with the 4-module quiet zone scanners need). The generator (`uqr`, ~5 kB)
	 * is loaded on demand.
	 */
	let { value, label }: { value: string; label: string } = $props();

	let matrix = $state<boolean[][] | null>(null);
	let failed = $state(false);

	$effect(() => {
		const text = value;
		let cancelled = false;
		matrix = null;
		failed = false;
		import('uqr')
			.then(({ encode }) => {
				if (!cancelled) matrix = encode(text, { ecc: 'M', border: 0 }).data;
			})
			.catch(() => {
				if (!cancelled) failed = true;
			});
		return () => {
			cancelled = true;
		};
	});

	const QUIET = 4;
	const size = $derived(matrix ? matrix.length + QUIET * 2 : 0);
	/** One path of horizontal runs keeps the markup small (a v6 code is ~1 kB). */
	const path = $derived.by(() => {
		if (!matrix) return '';
		let d = '';
		matrix.forEach((row, y) => {
			let x = 0;
			while (x < row.length) {
				if (!row[x]) {
					x++;
					continue;
				}
				const start = x;
				while (x < row.length && row[x]) x++;
				d += `M${start + QUIET} ${y + QUIET}h${x - start}v1h-${x - start}z`;
			}
		});
		return d;
	});
</script>

{#if matrix}
	<svg
		viewBox="0 0 {size} {size}"
		role="img"
		aria-label={label}
		shape-rendering="crispEdges"
		class="block h-auto w-full rounded-xl bg-white"
	>
		<path d={path} fill="#1a1033" />
	</svg>
{:else if failed}
	<p role="alert" class="p-4 text-center font-bold text-night">{m.lobby_qr_failed()}</p>
{:else}
	<div
		class="aspect-square w-full animate-pulse rounded-xl bg-night/10"
		role="status"
		aria-label={m.lobby_qr_loading()}
	></div>
{/if}
