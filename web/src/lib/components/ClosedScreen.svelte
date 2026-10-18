<script lang="ts">
	import { resolve } from '$app/paths';
	import Button from '$lib/components/Button.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import { errorMessage } from '$lib/errors';
	import type { ClosedReason } from '$lib/game/session';
	import { m } from '$lib/paraglide/messages';

	/**
	 * A session that ended for good. Every reason explains what happened and
	 * always offers the way Home; `replaced` and `join_rejected` also offer a
	 * second try (`onretry`), `unsupported_version` a reload.
	 */
	let {
		reason,
		joinError = null,
		onretry,
		onreload = () => location.reload()
	}: {
		reason: ClosedReason;
		/** The error code of a refused join (`join_rejected`). */
		joinError?: string | null;
		onretry?: () => void;
		onreload?: () => void;
	} = $props();

	const content = $derived.by(() => {
		switch (reason) {
			case 'room_gone':
				return { icon: '🍂', title: m.closed_room_gone_title(), text: m.closed_room_gone_text() };
			case 'kicked':
				return { icon: '🚪', title: m.closed_kicked_title(), text: m.closed_kicked_text() };
			case 'left':
				return { icon: '👋', title: m.closed_left_title(), text: m.closed_left_text() };
			case 'replaced':
				return { icon: '📱', title: m.closed_replaced_title(), text: m.closed_replaced_text() };
			case 'unsupported_version':
				return { icon: '✨', title: m.update_available(), text: m.closed_version_text() };
			case 'join_rejected':
				return {
					icon: '🙈',
					title: m.closed_rejected_title(),
					text:
						joinError === 'wrong_phase' ? m.closed_rejected_over() : errorMessage(joinError ?? '')
				};
		}
	});

	const action = $derived.by(() => {
		if (reason === 'replaced' && onretry)
			return { label: m.closed_replaced_action(), run: onretry };
		if (reason === 'join_rejected' && onretry) return { label: m.closed_retry(), run: onretry };
		if (reason === 'unsupported_version') return { label: m.reload(), run: onreload };
		return null;
	});

	const link =
		'flex h-14 w-full items-center justify-center rounded-2xl px-6 text-xl font-black shadow-pop transition active:translate-y-1 active:shadow-none';
</script>

<Screen label={content.title}>
	<div class="flex flex-1 flex-col items-center justify-center gap-4 text-center">
		<span class="animate-bounce-in text-7xl" aria-hidden="true">{content.icon}</span>
		<h1 class="text-3xl leading-tight font-black">{content.title}</h1>
		<p class="max-w-xs text-lg text-cream-muted">{content.text}</p>
	</div>
	{#snippet footer()}
		{#if action}
			<Button tone="banana" onclick={action.run}>{action.label}</Button>
		{/if}
		<a
			href={resolve('/')}
			class="{link} {action ? 'bg-night-3 text-cream' : 'bg-banana text-night'}"
		>
			{m.net_home()}
		</a>
	{/snippet}
</Screen>
