<script lang="ts">
	import { onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import ClosedScreen from '$lib/components/ClosedScreen.svelte';
	import ConnectingScreen from '$lib/components/ConnectingScreen.svelte';
	import ConnectionBanner from '$lib/components/ConnectionBanner.svelte';
	import JoinGate from '$lib/components/JoinGate.svelte';
	import { errorMessage } from '$lib/errors';
	import { WsRoomSession } from '$lib/game/client.svelte';
	import { clearRoomMark, hasRoomMark, markRoom } from '$lib/game/roomMark';
	import type { RoomSession } from '$lib/game/session';
	import { profile } from '$lib/profile.svelte';
	import RoomScreen from '$lib/screens/RoomScreen.svelte';
	import { toasts } from '$lib/toast.svelte';

	/**
	 * Shell of a room: gate -> connecting -> the phase screen (+ reconnect
	 * banner) -> closed screen. One instance per room code (the route keys it).
	 */
	let {
		code,
		createSession = (c: string): RoomSession => new WsRoomSession(c)
	}: {
		/** Canonical (upper-case) room code. */
		code: string;
		/** Replaced in tests. */
		createSession?: (code: string) => RoomSession;
	} = $props();

	/** A tab that already chose this room (and has a nickname) connects straight away; others see the gate. */
	function initialSession(): RoomSession | null {
		return hasRoomMark(code) && profile.nickname ? createSession(code) : null;
	}

	let session = $state.raw<RoomSession | null>(initialSession());

	function connect() {
		markRoom(code);
		session?.destroy();
		session = createSession(code);
	}

	function giveUp() {
		session?.destroy();
		session = null;
	}

	onDestroy(() => session?.destroy());

	// A translated toast for every NEW error (not for the ones that end the session: the closed screen says it).
	let seen: { session: RoomSession | null; seq: number } = { session: null, seq: 0 };
	$effect(() => {
		const s = session;
		const error = s?.lastError;
		if (!s) return;
		if (seen.session !== s) seen = { session: s, seq: 0 };
		if (!error || error.seq === seen.seq) return;
		seen.seq = error.seq;
		if (s.status !== 'closed') toasts.show(errorMessage(error.code));
	});

	// Leaving, being kicked, a vanished room or a refused join: this tab no longer "is in" the room.
	$effect(() => {
		if (session?.status !== 'closed') return;
		const reason = session.closedReason;
		if (reason !== 'replaced' && reason !== 'unsupported_version') clearRoomMark(code);
		if (reason === 'left') void goto(resolve('/'));
	});
</script>

{#if !session}
	<JoinGate {code} onjoin={connect} />
{:else if session.status === 'closed'}
	{#if session.closedReason && session.closedReason !== 'left'}
		<ClosedScreen
			reason={session.closedReason}
			joinError={session.joinError}
			onretry={session.closedReason === 'replaced' ? connect : giveUp}
		/>
	{/if}
{:else if session.view}
	<RoomScreen {session} />
	<ConnectionBanner {session} />
{:else}
	<ConnectingScreen {code} />
{/if}
