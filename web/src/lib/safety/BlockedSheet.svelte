<script lang="ts">
	import { onMount } from 'svelte';
	import Button from '$lib/components/Button.svelte';
	import { errorMessage } from '$lib/errors';
	import Sheet from '$lib/lobby/Sheet.svelte';
	import { m } from '$lib/paraglide/messages';
	import { toasts } from '$lib/toast.svelte';
	import { blocks as sharedBlocks, type BlocksStore } from './blocks.svelte';

	/**
	 * "Blocked players": the caller's own block list (`GET /api/blocks`) with an Unblock button each.
	 * Needs no terms. The cached list shows at once and is refreshed from the server.
	 */
	let { onclose, blocks = sharedBlocks }: { onclose: () => void; blocks?: BlocksStore } = $props();

	let working = $state<string | null>(null);
	let failure = $state<string | null>(null);

	onMount(() => void blocks.load());

	async function unblock(playerId: string, name: string) {
		if (working) return;
		working = playerId;
		failure = null;
		const result = await blocks.unblock(playerId);
		working = null;
		if (result.ok) toasts.show(m.blocked_unblocked({ name: name || m.blocked_unknown_name() }));
		else failure = result.code;
	}
</script>

<Sheet title={m.blocked_title()} {onclose}>
	<div class="flex flex-col gap-3 pb-2" data-testid="blocked-sheet">
		<p class="text-cream-muted">{m.blocked_intro()}</p>

		<div aria-live="polite">
			{#if blocks.status === 'loading' && blocks.list.length === 0}
				<p role="status" class="text-cream-muted">{m.blocked_loading()}</p>
			{:else if blocks.status === 'error'}
				<div class="flex flex-col gap-2">
					<p role="alert" class="font-bold text-strawberry">
						{m.blocked_load_failed({ reason: errorMessage(blocks.error ?? 'unknown') })}
					</p>
					<Button tone="plain" size="md" onclick={() => void blocks.load()}>
						{m.blocked_retry()}
					</Button>
				</div>
			{:else if blocks.list.length === 0}
				<p class="text-lg font-bold">{m.blocked_empty()}</p>
			{/if}
			{#if failure}
				<p role="alert" class="pt-2 font-bold text-strawberry">{errorMessage(failure)}</p>
			{/if}
		</div>

		{#if blocks.list.length > 0}
			<ul class="flex flex-col gap-2" aria-label={m.blocked_title()}>
				{#each blocks.list as entry (entry.playerId)}
					{@const name = entry.name || m.blocked_unknown_name()}
					<li
						class="flex min-h-14 items-center gap-2 rounded-xl bg-night py-1 pr-1 pl-3 ring-1 ring-night-3"
					>
						<span class="min-w-0 flex-1 truncate text-lg font-bold">{name}</span>
						<button
							type="button"
							disabled={working !== null}
							aria-busy={working === entry.playerId}
							aria-label={m.blocked_unblock_aria({ name })}
							onclick={() => unblock(entry.playerId, entry.name)}
							class="h-12 shrink-0 rounded-xl bg-night-3 px-4 font-extrabold transition active:translate-y-0.5 disabled:opacity-60"
						>
							{m.blocked_unblock()}
						</button>
					</li>
				{/each}
			</ul>
			<p class="text-sm text-cream-muted">{m.blocked_unblock_note()}</p>
		{/if}
	</div>
</Sheet>
