<script lang="ts">
	import { tick } from 'svelte';
	import AvatarBadge from '$lib/components/AvatarBadge.svelte';
	import { m } from '$lib/paraglide/messages';
	import type { Player } from '$lib/protocol';
	import SafetyButton from '$lib/safety/SafetyButton.svelte';
	import { diffPlayers } from './announce';
	import { uiAvatar } from './avatar';

	interface Props {
		players: readonly Player[];
		hostId: string;
		youId: string;
		maxPlayers: number;
		/** The host can remove the others (asks for confirmation first). */
		canKick: boolean;
		onkick: (playerId: string) => void;
	}
	let { players, hostId, youId, maxPlayers, canKick, onkick }: Props = $props();

	let confirming = $state<string | null>(null);
	let announcement = $state('');
	const kickButtons: Record<string, HTMLButtonElement | undefined> = {};

	let known: Pick<Player, 'id' | 'name'>[] | null = null;
	$effect(() => {
		const current = players.map((p) => ({ id: p.id, name: p.name }));
		if (known) {
			const { joined, left } = diffPlayers(known, current);
			const parts = [
				...joined.map((name) => m.lobby_player_joined({ name })),
				...left.map((name) => m.lobby_player_left({ name }))
			];
			if (parts.length > 0) announcement = parts.join('. ');
		}
		known = current;
		if (confirming && !current.some((p) => p.id === confirming)) confirming = null;
	});

	function focusOnMount(node: HTMLElement) {
		node.focus();
	}

	async function cancel(id: string) {
		confirming = null;
		await tick();
		kickButtons[id]?.focus();
	}

	function confirmKick(id: string) {
		confirming = null;
		onkick(id);
	}
</script>

<section aria-labelledby="lobby-players-title" class="flex flex-col gap-2">
	<div class="flex items-baseline justify-between gap-2 px-1">
		<h2 id="lobby-players-title" class="text-lg font-black">{m.lobby_players_title()}</h2>
		<span
			class="rounded-full bg-night-3 px-3 py-0.5 text-sm font-extrabold"
			aria-label={m.lobby_players_count_label({ count: players.length, max: maxPlayers })}
		>
			{players.length}/{maxPlayers}
		</span>
	</div>

	<p class="sr-only" role="status" aria-live="polite">{announcement}</p>

	<ul class="flex flex-col gap-1.5">
		{#each players as player (player.id)}
			{@const isYou = player.id === youId}
			<li
				class="flex min-h-14 animate-bounce-in flex-wrap items-center gap-x-2 gap-y-1 rounded-2xl bg-night-2/80 px-2 py-1 ring-1 ring-night-3"
			>
				<AvatarBadge avatar={uiAvatar(player.avatar)} size={40} />
				{#if confirming === player.id}
					<p class="min-w-0 flex-1 font-bold">{m.lobby_kick_prompt({ name: player.name })}</p>
					<div class="flex w-full gap-2 pb-1">
						<button
							type="button"
							use:focusOnMount
							onclick={() => cancel(player.id)}
							class="h-12 flex-1 rounded-xl bg-night-3 px-3 font-extrabold"
						>
							{m.lobby_cancel()}
						</button>
						<button
							type="button"
							aria-label={m.lobby_kick_confirm({ name: player.name })}
							onclick={() => confirmKick(player.id)}
							class="h-12 flex-1 rounded-xl bg-strawberry px-3 font-extrabold text-night"
						>
							{m.lobby_kick_yes()}
						</button>
					</div>
				{:else}
					<span class="min-w-0 flex-1 truncate text-lg font-bold">{player.name}</span>
					{#if player.id === hostId}
						<span role="img" aria-label={m.lobby_host_badge()} title={m.lobby_host_badge()}>👑</span
						>
					{/if}
					{#if isYou}
						<span class="rounded-full bg-grape px-2 py-0.5 text-xs font-extrabold text-night">
							{m.lobby_you()}
						</span>
					{/if}
					{#if !player.connected}
						<span
							role="img"
							aria-label={m.lobby_offline()}
							title={m.lobby_offline()}
							class="size-3 shrink-0 rounded-full bg-tangerine ring-2 ring-night"
						></span>
					{/if}
					<SafetyButton
						request={{ start: 'person', person: { id: player.id, name: player.name } }}
						label={m.report_player_aria({ name: player.name })}
					/>
					{#if canKick && !isYou}
						<button
							type="button"
							bind:this={kickButtons[player.id]}
							aria-label={m.lobby_kick({ name: player.name })}
							onclick={() => (confirming = player.id)}
							class="flex size-12 shrink-0 items-center justify-center rounded-xl text-cream-muted hover:text-strawberry"
						>
							<span aria-hidden="true">✕</span>
						</button>
					{/if}
				{/if}
			</li>
		{/each}
	</ul>

	{#if players.length <= 1}
		<p class="px-1 text-center text-base text-cream-muted">{m.lobby_empty_hint()}</p>
	{/if}
</section>
