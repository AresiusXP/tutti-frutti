<script lang="ts">
	import Sheet from './Sheet.svelte';

	/** Test harness: a sheet opened by a button, optionally with a second one on top of it. */
	let { onclosed }: { onclosed?: (which: string) => void } = $props();
	let outer = $state(false);
	let inner = $state(false);
</script>

<button type="button" onclick={() => (outer = true)}>open outer</button>

{#if outer}
	<Sheet
		title="Outer"
		onclose={() => {
			outer = false;
			inner = false;
			onclosed?.('outer');
		}}
	>
		<button type="button" onclick={() => (inner = true)}>open inner</button>
		{#if inner}
			<Sheet
				title="Inner"
				onclose={() => {
					inner = false;
					onclosed?.('inner');
				}}
			>
				inner text
			</Sheet>
		{/if}
	</Sheet>
{/if}
