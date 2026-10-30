<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { RoomSession } from '$lib/game/session';
	import { provideSafety, type SafetyRequest } from './context';
	import SafetySheet from './SafetySheet.svelte';
	import { canTarget } from './targets';

	/**
	 * Mounted once around the phase screens of a room (`RoomScreen`): it provides what the flag buttons
	 * need and shows the one safety sheet for whatever was asked. One host per room means the sheet is
	 * not lost when the game moves from one phase to the next.
	 */
	let { session, children }: { session: RoomSession; children: Snippet } = $props();

	let request = $state.raw<SafetyRequest | null>(null);

	provideSafety({
		get youId() {
			return session.view?.youId ?? '';
		},
		canTarget: (id) => canTarget(session.view?.youId ?? '', id),
		open: (next) => (request = next)
	});
</script>

{@render children()}

{#if request}
	{#key request}
		<SafetySheet {session} {request} onclose={() => (request = null)} />
	{/key}
{/if}
