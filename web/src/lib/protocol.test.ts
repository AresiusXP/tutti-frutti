import { describe, expect, it } from 'vitest';
import { DEFAULT_AVATAR } from './avatar';
import * as proto from './protocol';
import type {
	Avatar,
	Cmd,
	Envelope,
	ErrorBody,
	Event,
	JoinRequest,
	Lang,
	Listing,
	Phase,
	Pong,
	RoundView,
	State,
	Status,
	StopVoteView,
	View
} from './protocol';

// Compile-time exhaustiveness: Record<Union, true> fails to type-check when a
// member is missing (or unknown), so adding a Go constant breaks `npm run check`
// until this list is updated.
const phases: Record<Phase, true> = {
	lobby: true,
	letter: true,
	playing: true,
	grace: true,
	review: true,
	round_summary: true,
	final: true
};
const langs: Record<Lang, true> = { es: true, en: true, nl: true };
const statuses: Record<Status, true> = {
	empty: true,
	wrong_letter: true,
	rejected: true,
	valid: true
};
const commands: Record<Cmd, true> = {
	join: true,
	leave: true,
	update_settings: true,
	kick: true,
	start_game: true,
	stop_letter: true,
	set_answer: true,
	basta: true,
	ask_stop: true,
	vote_stop: true,
	vote_reject: true,
	propose_merge: true,
	vote_merge: true,
	review_nav: true,
	finish_review: true,
	next_round: true,
	end_game: true,
	rematch: true
};

const lobbyState = {
	code: 'KT7P',
	serverTime: 1_760_790_000_000,
	view: {
		phase: 'lobby',
		youId: 'guest-ana',
		hostId: 'guest-ana',
		maxPlayers: 10,
		players: [
			{
				id: 'guest-ana',
				name: 'Ana',
				avatar: { emoji: '🍓', color: 'strawberry' },
				connected: true,
				connectedAt: '2026-10-18T12:00:00Z',
				joinedSeq: 1,
				joinedRound: 1,
				left: false,
				totalScore: 0
			},
			{
				id: 'guest-beto',
				name: 'Beto',
				avatar: { emoji: '🍌', color: 'banana' },
				connected: true,
				connectedAt: '2026-10-18T12:00:05.123456789Z',
				joinedSeq: 2,
				joinedRound: 1,
				left: false,
				totalScore: 0
			}
		],
		settings: {
			lang: 'es',
			excludeHardLetters: true,
			rounds: 5,
			categories: [
				{ id: 'nombre' },
				{ id: 'pais' },
				{ id: 'animal' },
				{ id: 'comida' },
				{ id: 'color' },
				{ id: 'marca' },
				{ custom: 'Superhéroe' }
			],
			timeLimitSeconds: 0,
			stopVote: true
		},
		categoryLabels: ['Nombre', 'País', 'Animal', 'Comida', 'Color', 'Marca', 'Superhéroe'],
		usedLetters: [],
		roundNum: 0,
		history: []
	}
} satisfies State;

describe('protocol constants', () => {
	it('is protocol version 3 (vote to stop)', () => {
		expect(proto.Version).toBe(3);
	});

	it('has the vote-to-stop constants', () => {
		expect(proto.CmdAskStop).toBe('ask_stop');
		expect(proto.CmdVoteStop).toBe('vote_stop');
		expect(proto.EventStopAsked).toBe('stop_asked');
		expect(proto.EventStopRejected).toBe('stop_rejected');
		expect(proto.StopVoteDuration).toBe(15_000);
		expect([
			proto.CodeStopVoteDisabled,
			proto.CodeStopVotePending,
			proto.CodeNoStopVote,
			proto.CodeStopNotNeeded
		]).toEqual(['stop_vote_disabled', 'stop_vote_pending', 'no_stop_vote', 'stop_not_needed']);
	});

	it('has the WebSocket close codes', () => {
		expect([
			proto.ClosePolicy,
			proto.CloseTooBig,
			proto.CloseServiceRestart,
			proto.CloseReplaced,
			proto.CloseKicked,
			proto.CloseUnsupportedVersion,
			proto.CloseRoomClosed,
			proto.CloseLeft
		]).toEqual([1008, 1009, 1012, 4001, 4002, 4003, 4004, 4005]);
	});

	it('has the frame types', () => {
		expect(proto.TypePing).toBe('ping');
		expect(proto.TypePong).toBe('pong');
		expect(proto.TypeState).toBe('state');
		expect(proto.TypeEvent).toBe('event');
		expect(proto.TypeError).toBe('error');
	});

	it('has the guest cookie name and the transport limits', () => {
		expect(proto.GuestCookie).toBe('tf_guest');
		expect(proto.MaxMessageBytes).toBe(4096);
		// time constants are in milliseconds
		expect(proto.PingInterval).toBe(20_000);
		expect(proto.ReadIdleTimeout).toBe(60_000);
		expect(proto.WriteTimeout).toBe(10_000);
	});

	it('has stable snake_case protocol error codes', () => {
		const codes = [
			proto.ErrBadMessage,
			proto.ErrUnsupportedVersion,
			proto.ErrRoomNotFound,
			proto.ErrNotJoined,
			proto.ErrAlreadyJoined,
			proto.ErrRateLimited,
			proto.ErrMessageTooLarge,
			proto.ErrUnauthorized,
			proto.ErrRoomClosed,
			proto.ErrServerBusy
		];
		expect(new Set(codes).size).toBe(codes.length);
		for (const code of codes) expect(code).toMatch(/^[a-z]+(_[a-z]+)*$/);
		expect(proto.ErrUnsupportedVersion).toBe('unsupported_version');
	});

	it('has every command name of the design', () => {
		const names = [
			proto.CmdJoin,
			proto.CmdLeave,
			proto.CmdUpdateSettings,
			proto.CmdKick,
			proto.CmdStartGame,
			proto.CmdStopLetter,
			proto.CmdSetAnswer,
			proto.CmdBasta,
			proto.CmdAskStop,
			proto.CmdVoteStop,
			proto.CmdVoteReject,
			proto.CmdProposeMerge,
			proto.CmdVoteMerge,
			proto.CmdReviewNav,
			proto.CmdFinishReview,
			proto.CmdNextRound,
			proto.CmdEndGame,
			proto.CmdRematch
		];
		expect([...names].sort()).toEqual(Object.keys(commands).sort());
		// a wire "set_connected" must never become a command
		expect(names).not.toContain('set_connected');
	});

	it('has the engine and room event types', () => {
		const events = [
			proto.EventGameStarted,
			proto.EventRoundStarted,
			proto.EventLetterRevealed,
			proto.EventBasta,
			proto.EventStopAsked,
			proto.EventStopRejected,
			proto.EventTimeUp,
			proto.EventRoundLocked,
			proto.EventRoundScored,
			proto.EventKicked,
			proto.EventHostChanged,
			proto.EventGameOver,
			proto.EventRematch,
			proto.EventServerRestarting,
			proto.EventReplaced
		];
		expect(events).toEqual([
			'game_started',
			'round_started',
			'letter_revealed',
			'basta',
			'stop_asked',
			'stop_rejected',
			'time_up',
			'round_locked',
			'round_scored',
			'kicked',
			'host_changed',
			'game_over',
			'rematch',
			'server_restarting',
			'replaced'
		]);
	});
});

describe('generated unions', () => {
	it('Phase covers every phase of the state machine', () => {
		expect(Object.keys(phases)).toEqual([
			proto.PhaseLobby,
			proto.PhaseLetter,
			proto.PhasePlaying,
			proto.PhaseGrace,
			proto.PhaseReview,
			proto.PhaseRoundSummary,
			proto.PhaseFinal
		]);
		expect(Object.keys(phases)).toEqual([
			'lobby',
			'letter',
			'playing',
			'grace',
			'review',
			'round_summary',
			'final'
		]);
	});

	it('Lang and Status cover the engine values', () => {
		expect(Object.keys(langs)).toEqual([proto.LangES, proto.LangEN, proto.LangNL]);
		expect(Object.keys(statuses)).toEqual([
			proto.StatusEmpty,
			proto.StatusWrongLetter,
			proto.StatusRejected,
			proto.StatusValid
		]);
	});
});

describe('wire shapes', () => {
	it('accepts a lobby State with two players', () => {
		expect(lobbyState.view.players).toHaveLength(2);

		// what the WebSocket client sees after JSON.parse of a `state` frame
		const frame = JSON.parse(
			JSON.stringify({ t: proto.TypeState, v: proto.Version, d: lobbyState })
		) as Envelope;
		const state = frame.d as State;
		const view: View = state.view;
		expect(frame.t).toBe(proto.TypeState);
		expect(frame.v).toBe(proto.Version);
		expect(state.code).toBe('KT7P');
		expect(view.phase).toBe('lobby');
		expect(view.round).toBeUndefined();
		expect(view.players.map((p) => p.name)).toEqual(['Ana', 'Beto']);
	});

	it('types the optional fields of a playing round', () => {
		const round: RoundView = {
			number: 1,
			letter: 'M',
			participants: ['guest-ana', 'guest-beto'],
			answers: { 'guest-ana': ['Marta', '', '', '', '', ''] },
			filled: { 'guest-ana': 1, 'guest-beto': 0 },
			timeLimitEndsAt: '2026-10-18T12:03:00Z',
			reviewCategory: 0,
			rejectVotes: [],
			proposals: []
		};
		const playing: View = {
			...lobbyState.view,
			phase: 'playing',
			roundNum: 1,
			usedLetters: ['M'],
			round
		};
		expect(playing.round?.callerId).toBeUndefined();
		expect(playing.round?.graceEndsAt).toBeUndefined();
		expect(playing.round?.timeLimitEndsAt).toBe('2026-10-18T12:03:00Z');
	});

	it('types the open vote to stop of a playing round and the events around it', () => {
		const stopVote: StopVoteView = {
			callerId: 'guest-ana',
			endsAt: '2026-10-18T12:00:15Z',
			voters: ['guest-beto'],
			yes: []
		};
		const round: RoundView = {
			number: 1,
			letter: 'M',
			participants: ['guest-ana', 'guest-beto'],
			answers: {},
			filled: {},
			stopVote,
			reviewCategory: 0,
			rejectVotes: [],
			proposals: []
		};
		const rejected = {
			type: proto.EventStopRejected,
			playerId: 'guest-ana',
			name: 'Ana',
			reason: 'declined'
		} satisfies Event;
		const vote = { yes: true } satisfies proto.VoteStop;
		expect(round.stopVote?.voters).toEqual(['guest-beto']);
		expect(lobbyState.view.settings.stopVote).toBe(true);
		expect(rejected.reason).toBe('declined');
		expect(JSON.stringify(vote)).toBe('{"yes":true}');
		expect(JSON.stringify({} satisfies proto.AskStop)).toBe('{}');
	});

	it('types the frames of both directions', () => {
		const join = {
			code: 'KT7P',
			name: 'Ana',
			avatar: DEFAULT_AVATAR
		} satisfies JoinRequest;
		const avatar: Avatar = DEFAULT_AVATAR;
		const frames = [
			{ t: proto.CmdJoin, v: proto.Version, d: join },
			{ t: proto.TypePing, v: proto.Version }
		] satisfies Envelope[];
		const pong = { serverTime: 1_760_790_000_000 } satisfies Pong;
		const error = { code: proto.ErrNotJoined, ref: proto.CmdBasta } satisfies ErrorBody;
		const event = { type: proto.EventBasta, playerId: 'guest-ana', name: 'Ana' } satisfies Event;

		expect(frames.map((f) => f.t)).toEqual(['join', 'ping']);
		expect(avatar.emoji).toBe(DEFAULT_AVATAR.emoji);
		expect(pong.serverTime).toBeGreaterThan(0);
		expect(error.code).toBe('not_joined');
		expect(event.type).toBe('basta');
	});

	it('types the GET /api/categories listing', () => {
		const listing = {
			lang: 'es',
			groups: ['personas'],
			categories: [{ id: 'nombre', group: 'personas', label: 'Nombre', default: true }]
		} satisfies Listing;
		expect(listing.categories[0].default).toBe(true);
	});
});
