<script lang="ts">
	import Button from '$lib/components/Button.svelte';
	import { m } from '$lib/paraglide/messages';
	import { toasts } from '$lib/toast.svelte';
	import { copyText, shareInvite } from './invite';

	let { code, onqr }: { code: string; onqr: () => void } = $props();

	/** Codes have no vowels, so screen readers would stumble over them: spell them out. */
	const spelled = $derived(Array.from(code).join(' '));

	async function copyCode() {
		toasts.show((await copyText(code)) ? m.lobby_code_copied() : m.lobby_copy_failed());
	}

	async function share() {
		const outcome = await shareInvite(code);
		if (outcome === 'copied') toasts.show(m.lobby_share_copied());
		else if (outcome === 'failed') toasts.show(m.lobby_copy_failed());
	}
</script>

<section aria-label={m.lobby_code_label()} class="flex flex-col items-center gap-1">
	<button
		type="button"
		aria-label={m.lobby_copy_code({ code: spelled })}
		onclick={copyCode}
		class="flex min-h-20 items-center justify-center rounded-3xl bg-night-2/80 px-6 py-1 pl-[calc(1.5rem+0.16em)] font-mono text-[4.25rem] leading-none font-black tracking-[0.16em] text-banana ring-1 ring-night-3 transition active:scale-95"
	>
		{code}
	</button>
	<p class="text-sm font-bold text-cream-muted">{m.lobby_code_hint()}</p>
	<div class="mt-1 flex w-full gap-2">
		<Button size="md" tone="banana" onclick={share} class="flex-1">
			<span aria-hidden="true">📤</span>
			{m.lobby_share()}
		</Button>
		<Button size="md" tone="plain" block={false} onclick={onqr} aria-label={m.lobby_qr_open()}>
			<span aria-hidden="true">▦</span>
			{m.lobby_qr()}
		</Button>
	</div>
</section>
