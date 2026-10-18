import { m } from '$lib/paraglide/messages';
import type { View } from '$lib/protocol';

/** "Ronda 2 de 5", or "Ronda 2" when the game has no round limit (`settings.rounds === 0`). */
export function roundLabel(view: View): string {
	const n = view.roundNum || view.round?.number || 1;
	return view.settings.rounds > 0
		? m.play_round_of({ n, total: view.settings.rounds })
		: m.play_round({ n });
}
