<script lang="ts">
	/**
	 * Inline "are you sure?" bar for the footer (instead of a modal): a short
	 * message and two thumb-sized answers. The safe answer gets the focus.
	 */
	interface Props {
		message: string;
		yes: string;
		no: string;
		onyes: () => void;
		onno: () => void;
	}
	let { message, yes, no, onyes, onno }: Props = $props();

	function focusOnMount(node: HTMLElement) {
		node.focus();
	}
</script>

<div role="group" aria-label={message} class="flex flex-col gap-2 rounded-2xl bg-night-2 p-3">
	<p class="text-center font-bold">{message}</p>
	<div class="flex gap-2">
		<button
			type="button"
			use:focusOnMount
			class="h-12 flex-1 rounded-xl bg-night-3 px-3 font-black text-cream"
			onclick={onno}
		>
			{no}
		</button>
		<button
			type="button"
			class="h-12 flex-1 rounded-xl bg-strawberry px-3 font-black text-night"
			onclick={onyes}
		>
			{yes}
		</button>
	</div>
</div>
