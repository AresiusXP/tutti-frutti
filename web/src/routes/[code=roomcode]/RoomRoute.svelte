<script lang="ts">
	import { onDestroy, onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import ClosedScreen from '$lib/components/ClosedScreen.svelte';
	import ConnectingScreen from '$lib/components/ConnectingScreen.svelte';
	import ConnectionBanner from '$lib/components/ConnectionBanner.svelte';
	import JoinGate from '$lib/components/JoinGate.svelte';
	import { errorMessage } from '$lib/errors';
	import { WsRoomSession } from '$lib/game/client.svelte';
	import { clearRoomMark, hasRoomMark, markRoom } from '$lib/game/roomMark';
	import { historySettled } from '$lib/lobby/historyPop';
	import type { RoomSession } from '$lib/game/session';
	import { profile } from '$lib/profile.svelte';
	import { holdReload } from '$lib/pwa/update.svelte';
	import { clearRecentRoom, loadRecentRoom, rememberRecentRoom } from '$lib/resume/recent';
	import { blocks } from '$lib/safety/blocks.svelte';
	import { terms } from '$lib/safety/terms.svelte';
	import RoomScreen from '$lib/screens/RoomScreen.svelte';
	import { toasts } from '$lib/toast.svelte';

	/**
	 * Shell of a room: gate -> connecting -> the phase screen (+ reconnect
	 * banner) -> closed screen. One instance per room code (the route keys it).
	 *
	 * The terms: Join on the gate asks for the acceptance first (`terms.ensure`); a tab that is already
	 * in the room just connects, and when the server answers `terms_required` to the join (a new
	 * version, an expired acceptance) the gate opens and the join is attempted once more.
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

	function open() {
		markRoom(code);
		session?.destroy();
		session = createSession(code);
	}

	let attempt = 0;
	let destroyed = false;

	/** The player pressed Join (or "take the seat back"): connect once the terms are accepted. */
	function connect() {
		const mine = ++attempt;
		// The usual case, with nothing to wait for: connect in the same tick.
		if (terms.status === 'ready' && !terms.acceptanceRequired) return open();
		void terms.ensure().then((accepted) => {
			if (!destroyed && mine === attempt && accepted) open();
		});
	}

	function giveUp() {
		session?.destroy();
		session = null;
	}

	onDestroy(() => {
		destroyed = true;
		session?.destroy();
		if (blocks.expecting === code) blocks.expectRemoval(null);
	});

	// An update accepted in ANOTHER tab must not reload this one while a round is being played.
	onMount(() =>
		holdReload(() => {
			const phase = session?.status === 'closed' ? null : session?.view?.phase;
			return phase === 'playing' || phase === 'grace';
		})
	);

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

	/** "Block and leave" is in flight or done: however the socket closed, the player simply goes Home. */
	const blockedAway = $derived(session?.status === 'closed' && blocks.expecting === code);

	// A seat the server just confirmed (the first state of a connection): the game "Resume recent game" on Home
	// offers when this browser's process dies and the per-tab marker is lost. Only the code and the instance id.
	$effect(() => {
		if (session?.status !== 'connected') return;
		const instance = session.roomInstanceId;
		if (instance) rememberRecentRoom(code, instance);
	});

	// The instance the slot held for this code when the route opened (the room "Resume recent game" brought us
	// to). A session that closes before its first state (a refused join, a vanished room) has no instance id of
	// its own; this one stands in for it, so the ending of this attempt never erases the entry another tab wrote
	// for a newer room that reuses the code. With neither, this tab has no claim on the slot and leaves it alone.
	const instanceAtOpen = ((): string | null => {
		const held = loadRecentRoom();
		return held && held.code === code ? held.roomInstanceId : null;
	})();

	// Leaving, being kicked, a vanished room or a refused join: this tab no longer "is in" the room, and there is
	// nothing to resume. (`replaced` and a new version keep both: the seat is still ours, a reload comes back.)
	$effect(() => {
		if (session?.status !== 'closed') return;
		const reason = session.closedReason;
		if (reason !== 'replaced' && reason !== 'unsupported_version') {
			clearRoomMark(code);
			const instance = session.roomInstanceId ?? instanceAtOpen;
			if (instance) clearRecentRoom({ code, roomInstanceId: instance });
		}
		// A sheet that was open (the leave confirmation) pops its history entry as it goes away; a goto() that
		// started before that pop arrived would be cancelled by it, so Home waits for it.
		if (reason === 'left' || blockedAway) void historySettled().then(() => goto(resolve('/')));
	});

	// The server wants the terms (a new version was deployed, the acceptance expired): show them, then join
	// once more. A second refusal right after the acceptance is not retried: the closed screen says it.
	let termsRetries = $state(0);
	let termsBusy = $state(false);
	const needsTerms = $derived(
		session?.status === 'closed' &&
			session.closedReason === 'join_rejected' &&
			session.joinError === 'terms_required' &&
			termsRetries < 1
	);
	$effect(() => {
		if (session?.status === 'connected') termsRetries = 0;
	});
	$effect(() => {
		const closed = session;
		if (!needsTerms || !closed) return;
		untrack(() => void acceptThenRejoin(closed));
	});

	async function acceptThenRejoin(closed: RoomSession) {
		termsRetries++;
		termsBusy = true;
		terms.require();
		const accepted = await terms.ensure();
		termsBusy = false;
		if (destroyed || session !== closed) return;
		if (accepted) return open();
		clearRoomMark(code);
		giveUp();
	}
</script>

{#if !session}
	<JoinGate {code} onjoin={connect} />
{:else if session.status === 'closed'}
	{#if needsTerms || termsBusy}
		<ConnectingScreen {code} />
	{:else if blockedAway}
		<!-- going Home: nothing to show -->
	{:else if session.closedReason && session.closedReason !== 'left'}
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
