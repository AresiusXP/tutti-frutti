<script lang="ts">
	import type { RoomSession } from '$lib/game/session';
	import SafetyHost from '$lib/safety/SafetyHost.svelte';
	import Final from './Final.svelte';
	import Letter from './Letter.svelte';
	import Lobby from './Lobby.svelte';
	import Playing from './Playing.svelte';
	import Review from './Review.svelte';
	import Summary from './Summary.svelte';

	/**
	 * Maps the game phase to its screen. Used by the room route and by the dev
	 * playground. `grace` is part of Playing (the Basta overlay). The `SafetyHost` around it
	 * provides the report / block sheet that the flag buttons of the screens open.
	 */
	let { session }: { session: RoomSession } = $props();
	const phase = $derived(session.view?.phase);
</script>

<SafetyHost {session}>
	{#if phase === 'lobby'}
		<Lobby {session} />
	{:else if phase === 'letter'}
		<Letter {session} />
	{:else if phase === 'playing' || phase === 'grace'}
		<Playing {session} />
	{:else if phase === 'review'}
		<Review {session} />
	{:else if phase === 'round_summary'}
		<Summary {session} />
	{:else if phase === 'final'}
		<Final {session} />
	{/if}
</SafetyHost>
