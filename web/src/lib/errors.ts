import { m } from '$lib/paraglide/messages';

/**
 * Every code the UI can be told about: engine `Code*`, protocol `Err*`, the
 * client's own `not_connected`, and the REST client's `network` /
 * `forbidden_origin` / `invalid_lang`. Anything else shows `err_unknown`.
 */
const MESSAGES: Record<string, () => string> = {
	not_host: () => m.err_not_host(),
	wrong_phase: () => m.err_wrong_phase(),
	not_your_turn: () => m.err_not_your_turn(),
	lobby_full: () => m.err_lobby_full(),
	invalid_settings: () => m.err_invalid_settings(),
	basta_not_allowed: () => m.err_basta_not_allowed(),
	stop_vote_disabled: () => m.err_stop_vote_disabled(),
	stop_vote_pending: () => m.err_stop_vote_pending(),
	no_stop_vote: () => m.err_no_stop_vote(),
	stop_not_needed: () => m.err_stop_not_needed(),
	unknown_player: () => m.err_unknown_player(),
	not_participant: () => m.err_not_participant(),
	not_enough_players: () => m.err_not_enough_players(),
	kicked: () => m.err_kicked(),
	cannot_kick_self: () => m.err_cannot_kick_self(),
	invalid_name: () => m.err_invalid_name(),
	invalid_avatar: () => m.err_invalid_avatar(),
	invalid_category: () => m.err_invalid_category(),
	answer_too_long: () => m.err_answer_too_long(),
	self_vote: () => m.err_self_vote(),
	empty_answer: () => m.err_empty_answer(),
	already_same: () => m.err_already_same(),
	proposal_exists: () => m.err_proposal_exists(),
	invalid_merge: () => m.err_invalid_merge(),
	unknown_proposal: () => m.err_unknown_proposal(),
	judge_pending: () => m.err_judge_pending(),
	already_judged: () => m.err_already_judged(),
	nothing_to_judge: () => m.err_nothing_to_judge(),
	judge_unavailable: () => m.err_judge_unavailable(),
	login_required: () => m.err_login_required(),
	unknown_command: () => m.err_unknown_command(),
	bad_payload: () => m.err_bad_payload(),
	internal: () => m.err_internal(),
	bad_message: () => m.err_bad_message(),
	unsupported_version: () => m.err_unsupported_version(),
	room_not_found: () => m.err_room_not_found(),
	not_joined: () => m.err_not_joined(),
	already_joined: () => m.err_already_joined(),
	rate_limited: () => m.err_rate_limited(),
	message_too_large: () => m.err_message_too_large(),
	unauthorized: () => m.err_unauthorized(),
	room_closed: () => m.err_room_closed(),
	server_busy: () => m.err_server_busy(),
	not_connected: () => m.err_not_connected(),
	network: () => m.err_network(),
	forbidden_origin: () => m.err_forbidden_origin(),
	invalid_lang: () => m.err_invalid_lang()
};

/** The translated, user-facing sentence for an error code (current UI locale). */
export function errorMessage(code: string): string {
	return (MESSAGES[code] ?? (() => m.err_unknown()))();
}

/** Codes with a dedicated message (tests check that none of the server's codes is missing). */
export const KNOWN_ERROR_CODES: readonly string[] = Object.keys(MESSAGES);
