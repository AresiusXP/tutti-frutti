<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type RoomInfo } from '$lib/api';
	import { m } from '$lib/paraglide/messages';
	import { clearRecentRoom, loadRecentRoom, type RecentRoom } from './recent';
	import { verdictOf } from './verdict';

	/**
	 * Home's "Resume recent game" button: shown only when this browser remembers a game (`tf.recentRoom`, at most
	 * 24 h old) that the server still knows under the same room instance id, and it names the room code only.
	 *
	 * It asks the server first (`GET /api/rooms/{code}`, the same preflight as the invite gate) and shows nothing
	 * meanwhile, so a stale entry never flashes. A room that is gone, or whose code now belongs to another room,
	 * is forgotten and the button never appears. A failed check (offline, busy, slow) is not fatal: the button is
	 * shown anyway and the room route, which never gives up, reconnects when it can. Full, finished and running
	 * games are offered too: the tap only goes to the room route and the server decides, so a player who is still
	 * seated gets the seat back (guest cookie) and a stranger is refused there, gracefully.
	 *
	 * `onresume` is the same intent as Join on Home (mark this tab, open `/CODE`); nothing happens before the tap.
	 * `onshown` tells Home that the card is on screen: on a short phone Home drops its tagline to keep fitting.
	 */
	let {
		onresume,
		onshown,
		load = (code: string, signal: AbortSignal) => api.getRoom(code, { signal })
	}: {
		onresume: (code: string) => void;
		onshown?: () => void;
		/** Room preflight; replaced in tests. */
		load?: (code: string, signal: AbortSignal) => Promise<RoomInfo>;
	} = $props();

	/** A check that takes longer than this counts as failed (the card is offered unverified). */
	const CHECK_TIMEOUT_MS = 5000;

	let offered = $state<RecentRoom | null>(null);

	onMount(() => {
		const recent = loadRecentRoom();
		if (!recent) return;
		let decided = false;
		const abort = new AbortController();
		const decide = (result: { info: RoomInfo } | { error: unknown }) => {
			if (decided) return;
			decided = true;
			clearTimeout(timer);
			if (verdictOf(recent, result) === 'forget') {
				clearRecentRoom({ code: recent.code, roomInstanceId: recent.roomInstanceId });
			} else {
				offered = recent;
				onshown?.();
			}
		};
		const timer = setTimeout(() => {
			abort.abort();
			decide({ error: new Error('timeout') });
		}, CHECK_TIMEOUT_MS);
		void (async () => {
			try {
				decide({ info: await load(recent.code, abort.signal) });
			} catch (error) {
				decide({ error });
			}
		})();
		return () => {
			decided = true;
			clearTimeout(timer);
			abort.abort();
		};
	});
</script>

{#if offered}
	{@const code = offered.code}
	<button
		type="button"
		onclick={() => onresume(code)}
		class="flex min-h-12 items-center justify-between gap-3 rounded-2xl bg-night-2 px-4 text-left font-bold text-cream ring-2 ring-banana/60 transition active:bg-night-3"
	>
		<span class="flex min-w-0 items-center gap-2">
			<span aria-hidden="true">↩</span>
			<span class="min-w-0 leading-tight">{m.rooms_resume()}</span>
		</span>
		<span class="shrink-0 font-black tracking-[0.2em] text-banana">{code}</span>
	</button>
{/if}
