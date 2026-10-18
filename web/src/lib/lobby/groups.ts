import { m } from '$lib/paraglide/messages';

/** Picker group titles follow the UI language (`lobby_group_<id>`); an unknown group shows its id. */
const TITLES: Record<string, () => string> = {
	personas: () => m.lobby_group_personas(),
	lugares: () => m.lobby_group_lugares(),
	naturaleza: () => m.lobby_group_naturaleza(),
	comida: () => m.lobby_group_comida(),
	cosas: () => m.lobby_group_cosas(),
	marcas: () => m.lobby_group_marcas(),
	cultura: () => m.lobby_group_cultura(),
	trabajo: () => m.lobby_group_trabajo(),
	divertidas: () => m.lobby_group_divertidas()
};

export function groupTitle(id: string): string {
	return TITLES[id]?.() ?? id;
}
