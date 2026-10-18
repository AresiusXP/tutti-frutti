<script lang="ts">
	import Button from '$lib/components/Button.svelte';
	import { m } from '$lib/paraglide/messages';
	import { inviteUrl } from './invite';
	import QrCode from './QrCode.svelte';
	import Sheet from './Sheet.svelte';

	let { code, onclose }: { code: string; onclose: () => void } = $props();
	const url = $derived(inviteUrl(code));
</script>

<Sheet title={m.lobby_qr_title()} {onclose}>
	<div class="flex flex-col items-center gap-3 pb-2">
		<div class="w-full max-w-[17rem] rounded-3xl bg-white p-3 shadow-pop">
			<QrCode value={url} label={m.lobby_qr_alt({ code })} />
		</div>
		<p class="text-xl font-black">{m.lobby_qr_caption()}</p>
		<p class="font-mono text-sm break-all text-cream-muted">{url}</p>
	</div>
	{#snippet footer()}
		<Button tone="plain" onclick={onclose}>{m.lobby_close()}</Button>
	{/snippet}
</Sheet>
