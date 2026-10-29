<script lang="ts">
	import { untrack } from 'svelte';
	import Button from '$lib/components/Button.svelte';
	import Screen from '$lib/components/Screen.svelte';
	import type { RoomSession } from '$lib/game/session';
	import { catalog } from '$lib/lobby/catalog.svelte';
	import PlayerList from '$lib/lobby/PlayerList.svelte';
	import QrSheet from '$lib/lobby/QrSheet.svelte';
	import RoomHeader from '$lib/lobby/RoomHeader.svelte';
	import SettingsEditor from '$lib/lobby/SettingsEditor.svelte';
	import SettingsSummary from '$lib/lobby/SettingsSummary.svelte';
	import Sheet from '$lib/lobby/Sheet.svelte';
	import { createSettingsSync } from '$lib/lobby/sync.svelte';
	import { m } from '$lib/paraglide/messages';

	/**
	 * The lobby (design §6.2): the huge room code with Share / QR, the players,
	 * the settings (editable for the host, read-only chips for everybody else)
	 * and the thumb-reachable Start / Leave actions.
	 */
	let { session }: { session: RoomSession } = $props();

	const MIN_PLAYERS = 2;
	const sync = createSettingsSync(() => session);

	const view = $derived(session.view);
	const isHost = $derived(session.isHost);
	const hostName = $derived(view?.players.find((p) => p.id === view.hostId)?.name ?? '');
	const canStart = $derived(session.players.length >= MIN_PLAYERS);
	const settings = $derived(isHost ? (sync.settings ?? view?.settings) : view?.settings);

	/** Labels of what is on screen: the server's, or (while an edit is unconfirmed) rebuilt from the catalog. */
	const labels = $derived.by(() => {
		if (!view || !settings) return [];
		if (!isHost || !sync.saving) return view.categoryLabels;
		const known = new Map(
			view.settings.categories.map((ref, i) => [ref.id, view.categoryLabels[i]])
		);
		const listing = catalog.listing(settings.lang);
		return settings.categories.map(
			(ref) =>
				ref.custom ??
				listing?.categories.find((c) => c.id === ref.id)?.label ??
				known.get(ref.id) ??
				ref.id ??
				''
		);
	});

	let qrOpen = $state(false);
	let leaveOpen = $state(false);

	let seenError = untrack(() => session.lastError?.seq ?? 0);
	let startFailed = $state(false);
	$effect(() => {
		const error = session.lastError;
		if (!error || error.seq <= seenError) return;
		seenError = error.seq;
		if (error.ref === 'start_game') startFailed = true;
	});

	function start() {
		if (!canStart) return;
		startFailed = false;
		session.startGame();
	}

	function leave() {
		leaveOpen = false;
		session.leave();
	}
</script>

{#if view && settings}
	<Screen label={m.lobby_screen_label()}>
		{#snippet header()}
			<h1 class="sr-only">{m.lobby_screen_label()}</h1>
			<RoomHeader code={session.code} onqr={() => (qrOpen = true)} />
		{/snippet}

		{#if session.visibility === 'public'}
			<p class="text-center text-sm text-cream-muted">{m.rooms_casual()}</p>
		{/if}
		<PlayerList
			players={session.players}
			hostId={view.hostId}
			youId={view.youId}
			maxPlayers={view.maxPlayers}
			canKick={isHost}
			onkick={(id) => session.kick(id)}
		/>

		{#if isHost}
			<SettingsEditor {settings} {labels} onchange={sync.commit} failed={sync.failed} />
		{:else}
			<SettingsSummary {settings} {labels} {hostName} />
		{/if}

		{#snippet footer()}
			{#if isHost}
				{#if startFailed}
					<p role="alert" class="text-center text-sm font-bold text-strawberry">
						{m.lobby_start_error()}
					</p>
				{:else if !canStart}
					<p id="lobby-start-hint" class="text-center text-sm text-cream-muted">
						{m.lobby_start_hint({ min: MIN_PLAYERS })}
					</p>
				{/if}
			{/if}
			<div class="flex items-stretch gap-2">
				<Button
					size="lg"
					tone="plain"
					block={false}
					aria-haspopup="dialog"
					onclick={() => (leaveOpen = true)}
					class="bg-transparent! px-4! text-base! font-bold! text-cream-muted! shadow-none! ring-1 ring-night-3"
				>
					{m.lobby_leave()}
				</Button>
				{#if isHost}
					<Button
						tone="lime"
						disabled={!canStart}
						aria-describedby={canStart ? undefined : 'lobby-start-hint'}
						onclick={start}
						class="flex-1"
					>
						{m.lobby_start()}
					</Button>
				{:else}
					<p
						role="status"
						class="flex min-h-14 flex-1 items-center justify-center gap-2 rounded-2xl bg-night-2 px-3 text-center text-base leading-tight font-extrabold ring-1 ring-night-3"
					>
						<span class="size-2.5 shrink-0 animate-pulse rounded-full bg-banana" aria-hidden="true"
						></span>
						{m.lobby_waiting_host({ name: hostName })}
					</p>
				{/if}
			</div>
		{/snippet}
	</Screen>

	{#if qrOpen}
		<QrSheet code={session.code} onclose={() => (qrOpen = false)} />
	{/if}

	{#if leaveOpen}
		<Sheet title={m.lobby_leave_title()} onclose={() => (leaveOpen = false)}>
			<p class="pb-2 text-lg">
				{isHost ? m.lobby_leave_body_host() : m.lobby_leave_body()}
			</p>
			{#snippet footer()}
				<div class="flex gap-2">
					<Button tone="plain" onclick={() => (leaveOpen = false)}>{m.lobby_leave_stay()}</Button>
					<Button tone="strawberry" onclick={leave}>{m.lobby_leave_confirm()}</Button>
				</div>
			{/snippet}
		</Sheet>
	{/if}
{/if}
