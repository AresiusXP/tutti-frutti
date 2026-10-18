package game

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestParseCommand(t *testing.T) {
	settings := DefaultSettings()
	settings.Rounds = 3
	settingsJSON, _ := json.Marshal(settings)

	tests := []struct {
		typ     string
		payload string
		want    Command
	}{
		{"join", `{"name":"Ana","avatar":{"emoji":"🦊","color":"#f00"}}`, Join{Name: "Ana", Avatar: Avatar{Emoji: "🦊", Color: "#f00"}}},
		{"leave", ``, Leave{}},
		{"update_settings", `{"settings":` + string(settingsJSON) + `}`, UpdateSettings{Settings: settings}},
		{"kick", `{"playerId":"p2"}`, Kick{PlayerID: "p2"}},
		{"start_game", `{}`, StartGame{}},
		{"stop_letter", `null`, StopLetter{}},
		{"set_answer", `{"category":2,"text":"Argentina"}`, SetAnswer{Category: 2, Text: "Argentina"}},
		{"basta", ``, Basta{}},
		{"ask_stop", ``, AskStop{}},
		{"vote_stop", `{"yes":true}`, VoteStop{Yes: true}},
		{"vote_stop", `{"yes":false}`, VoteStop{}},
		{"vote_reject", `{"category":1,"targetPlayer":"p3"}`, VoteReject{Category: 1, Target: "p3"}},
		{"vote_reject", `{"category":1,"targetPlayer":"p3","withdraw":true}`, VoteReject{Category: 1, Target: "p3", Withdraw: true}},
		{"propose_merge", `{"category":4,"playerA":"p1","playerB":"p2"}`, ProposeMerge{Category: 4, PlayerA: "p1", PlayerB: "p2"}},
		{"vote_merge", `{"proposalId":7,"yes":true}`, VoteMerge{ProposalID: 7, Yes: true}},
		{"review_nav", `{"category":3}`, ReviewNav{Category: 3}},
		{"finish_review", ``, FinishReview{}},
		{"next_round", ``, NextRound{}},
		{"end_game", ``, EndGame{}},
		{"rematch", ``, Rematch{}},
	}
	covered := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			got, err := ParseCommand(tt.typ, json.RawMessage(tt.payload))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseCommand(%q) = %#v, want %#v", tt.typ, got, tt.want)
			}
			if got.Type() != tt.typ {
				t.Errorf("Type() = %q, want %q", got.Type(), tt.typ)
			}

			// A marshalled command parses back to itself.
			b, err := json.Marshal(tt.want)
			if err != nil {
				t.Fatal(err)
			}
			back, err := ParseCommand(tt.typ, b)
			if err != nil || !reflect.DeepEqual(back, tt.want) {
				t.Errorf("round trip = %#v, %v; want %#v", back, err, tt.want)
			}
		})
		covered[tt.typ] = true
	}

	for _, typ := range []string{
		CmdJoin, CmdLeave, CmdUpdateSettings, CmdKick, CmdStartGame, CmdStopLetter, CmdSetAnswer,
		CmdBasta, CmdAskStop, CmdVoteStop, CmdVoteReject, CmdProposeMerge, CmdVoteMerge, CmdReviewNav, CmdFinishReview,
		CmdNextRound, CmdEndGame, CmdRematch,
	} {
		if !covered[typ] {
			t.Errorf("command %q has no ParseCommand case", typ)
		}
	}
}

func TestParseCommandErrors(t *testing.T) {
	tests := []struct {
		name    string
		typ     string
		payload string
		want    error
	}{
		{"unknown type", "teleport", `{}`, ErrUnknownCommand},
		{"ping is not an engine command", "ping", ``, ErrUnknownCommand},
		{"set_connected is rejected from the wire", "set_connected", `{"connected":true}`, ErrUnknownCommand},
		{"set_connected without payload", "set_connected", ``, ErrUnknownCommand},
		{"empty type", "", `{}`, ErrUnknownCommand},
		{"malformed json", "set_answer", `{"category":`, ErrBadPayload},
		{"wrong field type", "set_answer", `{"category":"one"}`, ErrBadPayload},
		{"not an object", "kick", `[1,2]`, ErrBadPayload},
		{"vote_stop with a string", "vote_stop", `{"yes":"yes"}`, ErrBadPayload},
		{"ask_stop with an array", "ask_stop", `[]`, ErrBadPayload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := ParseCommand(tt.typ, json.RawMessage(tt.payload))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if cmd != nil {
				t.Errorf("cmd = %#v, want nil", cmd)
			}
		})
	}
}
