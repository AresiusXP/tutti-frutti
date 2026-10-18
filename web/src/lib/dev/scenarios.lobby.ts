import { makeSettings } from '$lib/game/fixtures';
import { catalog } from '$lib/lobby/catalog.svelte';
import { fixtureFetch, labelsFor, type FixtureFetchOptions } from '$lib/lobby/fixtures';
import { lobbyView, players, ServerLikeSession, type ServerLikeOptions } from '$lib/lobby/testing';
import type { CategoryRef, Settings } from '$lib/protocol';
import type { Scenario } from './scenarios';

/**
 * Every scenario starts from a clean catalog cache served from the real
 * catalog fixture. The playground calls factories inside a `$derived`, where
 * state may not be written, hence the microtask.
 */
function scenario(
	build: () => { settings?: Partial<Settings>; players?: number; you?: string },
	options: { server?: ServerLikeOptions; fetch?: FixtureFetchOptions } = {}
): Scenario {
	return () => {
		const fetcher = fixtureFetch(options.fetch);
		queueMicrotask(() => catalog.reset(fetcher));
		const spec = build();
		const settings = makeSettings(spec.settings);
		const view = lobbyView(spec.players ?? 3, spec.you ?? 'p1', {
			settings,
			categoryLabels: labelsFor(settings.lang, settings.categories)
		});
		return new ServerLikeSession(view, options.server);
	};
}

const ids = (...list: string[]): CategoryRef[] => list.map((id) => ({ id }));

const TEN: CategoryRef[] = [
	...ids('nombre', 'pais', 'animal', 'comida', 'color', 'marca', 'ciudad', 'pelicula'),
	{ custom: 'Algo que da risa' },
	{ custom: 'Excusa de mi tío' }
];

export const scenarios: Record<string, Scenario> = {
	'lobby-host': scenario(() => ({})),
	'lobby-host-alone': scenario(() => ({ players: 1 })),
	'lobby-host-slow-server': scenario(() => ({}), { server: { latencyMs: 1200 } }),
	'lobby-host-rejected': scenario(() => ({}), { server: { rejectSettings: true } }),
	'lobby-guest': scenario(() => ({ you: 'p2', settings: { timeLimitSeconds: 90 } })),
	'lobby-host-stopvote-off': scenario(() => ({ settings: { stopVote: false } })),
	'lobby-guest-stopvote-off': scenario(() => ({
		you: 'p2',
		settings: { timeLimitSeconds: 90, stopVote: false }
	})),
	'lobby-full': scenario(() => ({ players: 10 })),
	'lobby-english': scenario(() => ({
		settings: { lang: 'en', rounds: 0, timeLimitSeconds: 120 }
	})),
	'lobby-english-guest': scenario(() => ({
		you: 'p2',
		settings: { lang: 'en', timeLimitSeconds: 90 }
	})),
	'lobby-dutch': scenario(() => ({
		settings: { lang: 'nl', rounds: 0, timeLimitSeconds: 120 }
	})),
	'lobby-dutch-guest': scenario(() => ({
		you: 'p2',
		settings: { lang: 'nl', timeLimitSeconds: 90 }
	})),
	// the ten longest Dutch labels (e.g. "Iets wat je meeneemt naar het strand")
	'lobby-dutch-long': scenario(() => ({
		settings: {
			lang: 'nl',
			categories: ids(
				'algo_playa',
				'excusa_tarde',
				'adjetivo',
				'lugar_turistico',
				'mascota',
				'ropa_accesorio',
				'util_escolar',
				'superheroe_villano',
				'algo_heladera',
				'provincia_estado'
			)
		}
	})),
	'lobby-many-categories': scenario(() => ({ settings: { categories: TEN, rounds: 8 } })),
	'lobby-min-categories': scenario(() => ({
		settings: { categories: ids('nombre', 'pais', 'animal') }
	})),
	'lobby-catalog-loading': scenario(() => ({}), { fetch: { hang: true } }),
	'lobby-catalog-error': scenario(() => ({}), { fetch: { failFirst: 2 } }),
	'lobby-offline-player': scenario(() => ({ players: 3 }))
};

// A disconnected seat for the offline scenario.
const base = scenarios['lobby-offline-player'];
scenarios['lobby-offline-player'] = () => {
	const session = base();
	if (session.view) {
		session.setView({
			...session.view,
			players: players(3, { p3: { connected: false } })
		});
	}
	return session;
};
