/**
 * Helpers for lobby tests and the dev playground: a `FakeSession` that behaves
 * like the server for settings and kicks (applies them to the view, optionally
 * after a delay, or refuses them).
 */
import { FakeSession } from '$lib/game/fake.svelte';
import { makeView, player } from '$lib/game/fixtures';
import type { Player, Settings, View } from '$lib/protocol';
import { labelsFor } from './fixtures';

export interface ServerLikeOptions {
	/** Milliseconds before the server "answers" an update (default: immediately). */
	latencyMs?: number;
	/** Refuse every `update_settings` with `invalid_settings`. */
	rejectSettings?: boolean;
	/** Never answer an `update_settings` (a lost or very slow server). */
	unresponsive?: boolean;
}

export class ServerLikeSession extends FakeSession {
	options: ServerLikeOptions;

	constructor(view: View | null = null, options: ServerLikeOptions = {}) {
		super(view);
		this.options = options;
	}

	override updateSettings(settings: Settings) {
		super.updateSettings(settings);
		if (this.options.unresponsive) return;
		const apply = () => {
			if (this.options.rejectSettings) {
				this.fail('invalid_settings', 'update_settings');
				return;
			}
			if (!this.view) return;
			this.setView({
				...this.view,
				settings,
				categoryLabels: labelsFor(settings.lang, settings.categories)
			});
		};
		if (this.options.latencyMs) setTimeout(apply, this.options.latencyMs);
		else apply();
	}

	override kick(playerId: string) {
		super.kick(playerId);
		if (!this.view) return;
		this.setView({ ...this.view, players: this.view.players.filter((p) => p.id !== playerId) });
	}
}

const NAMES = ['Ana', 'Beto', 'Caro', 'Dani', 'Eli', 'Fede', 'Gabi', 'Hugo', 'Inés', 'Joaquín'];
const EMOJIS = ['🍌', '🍊', '🍓', '🍇', '🍉', '🍍'];
const COLORS = ['banana', 'orange', 'strawberry', 'lime', 'grape', 'blueberry'];

/** `n` players p1..pn with palette avatars; `p1` is the host. */
export function players(n: number, over: Record<string, Partial<Player>> = {}): Player[] {
	return NAMES.slice(0, n).map((name, i) =>
		player(`p${i + 1}`, name, {
			avatar: { emoji: EMOJIS[i % EMOJIS.length], color: COLORS[(i + 2) % COLORS.length] },
			...over[`p${i + 1}`]
		})
	);
}

/** A lobby view for `n` players; `you` is the player id of the viewer. */
export function lobbyView(n = 3, you = 'p1', over: Partial<View> = {}): View {
	const list = players(n);
	return makeView({ players: list, youId: you, hostId: 'p1', ...over });
}
