<script lang="ts">
	import type { RoomSession } from '$lib/game/session';

	/** Stands in for the phase screens in RoomRoute tests (they are owned and tested elsewhere). */
	let { session }: { session: RoomSession } = $props();
</script>

<p data-testid="room-screen">{session.view?.phase}:{session.players.length}</p>
