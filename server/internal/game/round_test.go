package game

import (
	"strings"
	"testing"
	"time"
)

func TestStopLetterFromElapsedTime(t *testing.T) {
	// es without hard letters: A B C D E F G H I J L M N O P R S T U V
	tests := []struct {
		name    string
		elapsed time.Duration
		want    string
	}{
		{"immediately", 0, "A"},
		{"119ms", ms(119), "A"},
		{"120ms", ms(120), "B"},
		{"239ms", ms(239), "B"},
		{"240ms", ms(240), "C"},
		{"one full lap (20 letters)", ms(2400), "A"},
		{"a lap and a bit", ms(2400 + 119), "A"},
		{"a lap and 120ms", ms(2400 + 120), "B"},
		{"just before the auto-stop", ChooserTimeout - ms(1), "D"}, // 83 % 20 = 3
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newLobby(t, 2)
			apply(t, g, tStart, "p1", StartGame{})
			events := apply(t, g, tStart.Add(tt.elapsed), "p1", StopLetter{})

			if g.Phase != PhasePlaying || g.Round.Letter != tt.want {
				t.Fatalf("phase %s letter %q, want playing %q", g.Phase, g.Round.Letter, tt.want)
			}
			assertStrings(t, "UsedLetters", g.UsedLetters, []string{tt.want})
			if e, ok := hasEvent(events, EventLetterRevealed); !ok || e.Letter != tt.want || e.Round != 1 {
				t.Errorf("events = %+v", events)
			}
		})
	}
}

func TestStopLetterGuards(t *testing.T) {
	g := newLobby(t, 3)
	expectErr(t, g, tStart, "p1", StopLetter{}, ErrWrongPhase)
	apply(t, g, tStart, "p1", StartGame{})
	expectErr(t, g, tStart.Add(time.Second), "p2", StopLetter{}, ErrNotYourTurn)
	expectErr(t, g, tStart.Add(time.Second), "p3", StopLetter{}, ErrNotYourTurn)
	apply(t, g, tStart.Add(time.Second), "p1", StopLetter{})
	expectErr(t, g, tStart.Add(2*time.Second), "p1", StopLetter{}, ErrWrongPhase)
}

func TestStopLetterBeforeStartUsesFirstLetter(t *testing.T) {
	g := newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart.Add(-time.Second), "p1", StopLetter{})
	if g.Round.Letter != "A" {
		t.Errorf("letter = %q, a negative elapsed time gives the first letter", g.Round.Letter)
	}
}

func TestLetterSkipsUsedAndHardLetters(t *testing.T) {
	g := newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	g.UsedLetters = []string{"A", "B"} // pretend two rounds were already played
	apply(t, g, tStart, "p1", StopLetter{})
	if g.Round.Letter != "C" {
		t.Errorf("letter = %q, want C (A and B are used)", g.Round.Letter)
	}

	// K is hard in Spanish: after J comes L.
	g = newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart.Add(9*LetterTickDuration), "p1", StopLetter{}) // index 9 -> J
	if g.Round.Letter != "J" {
		t.Fatalf("letter = %q, want J", g.Round.Letter)
	}
	g = newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart.Add(10*LetterTickDuration), "p1", StopLetter{}) // index 10 -> L, K is skipped
	if g.Round.Letter != "L" {
		t.Errorf("letter = %q, want L", g.Round.Letter)
	}
}

func TestChooserAutoStop(t *testing.T) {
	g := newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})

	deadline, ok := g.NextDeadline()
	if !ok || !deadline.Equal(tStart.Add(ChooserTimeout)) {
		t.Fatalf("NextDeadline = %v, %v", deadline, ok)
	}
	if events := g.Tick(deadline.Add(-time.Nanosecond)); len(events) != 0 || g.Phase != PhaseLetter {
		t.Fatalf("Tick just before the deadline: events %v phase %s", events, g.Phase)
	}

	events := g.Tick(deadline)
	if g.Phase != PhasePlaying || g.Round.Letter != "D" {
		t.Fatalf("phase %s letter %q, want playing D (LetterAt at 10 s)", g.Phase, g.Round.Letter)
	}
	if e, ok := hasEvent(events, EventLetterRevealed); !ok || e.Letter != "D" {
		t.Errorf("events = %+v", events)
	}
	assertStrings(t, "UsedLetters", g.UsedLetters, []string{"D"})
	if _, ok := g.NextDeadline(); ok {
		t.Error("playing without a time limit has no deadline")
	}
}

func TestChooserAutoStopWhenTickIsLate(t *testing.T) {
	// A late tick still derives the letter from the 10 s timeout, not from now.
	g := newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	g.Tick(tStart.Add(time.Hour))
	if g.Round.Letter != "D" {
		t.Errorf("letter = %q, want D", g.Round.Letter)
	}
	if g.Phase != PhasePlaying {
		t.Errorf("phase = %s", g.Phase)
	}
}

func TestApplyRunsDueTransitionsFirst(t *testing.T) {
	g := newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})

	// The chooser taps STOP exactly at the 10 s deadline: the server already
	// auto-stopped, so the tap is a wrong-phase command and the letter is D.
	events, err := g.Apply(tStart.Add(ChooserTimeout), "p1", StopLetter{})
	if err != ErrWrongPhase {
		t.Fatalf("err = %v, want ErrWrongPhase", err)
	}
	if e, ok := hasEvent(events, EventLetterRevealed); !ok || e.Letter != "D" {
		t.Errorf("events = %+v, the auto-stop event must still be reported", events)
	}

	// One nanosecond earlier the tap wins and gets the letter for its elapsed time.
	g = newLobby(t, 2)
	apply(t, g, tStart, "p1", StartGame{})
	apply(t, g, tStart.Add(ChooserTimeout-time.Nanosecond), "p1", StopLetter{})
	if g.Round.Letter != "D" {
		t.Errorf("letter = %q", g.Round.Letter)
	}
}

func TestSetAnswer(t *testing.T) {
	now := tStart.Add(time.Second)
	t.Run("stores the trimmed answer and can clear it", func(t *testing.T) {
		g := newPlaying(t, 2, nil)
		apply(t, g, now, "p2", SetAnswer{Category: 1, Text: "  Argentina \n"})
		if got := g.Round.Answers["p2"][1]; got != "Argentina" {
			t.Errorf("answer = %q", got)
		}
		apply(t, g, now, "p2", SetAnswer{Category: 1, Text: "   "})
		if got := g.Round.Answers["p2"][1]; got != "" {
			t.Errorf("answer = %q, want cleared", got)
		}
		if got := g.Round.Answers["p1"][1]; got != "" {
			t.Errorf("another player's answer changed: %q", got)
		}
	})
	t.Run("invisible characters are stripped, so a hidden answer is not progress", func(t *testing.T) {
		g := newPlaying(t, 2, nil)
		apply(t, g, now, "p1", SetAnswer{Category: 0, Text: "\u200b\u200d \ufeff"})
		if got := g.Round.Answers["p1"][0]; got != "" {
			t.Errorf("answer = %q, want empty", got)
		}
		if got := g.View("p2").Round.Filled["p1"]; got != 0 {
			t.Errorf("filled = %d, an answer of invisible characters is not filled in", got)
		}
		apply(t, g, now, "p1", SetAnswer{Category: 1, Text: "\u200bAr\u00adgentina "})
		if got := g.Round.Answers["p1"][1]; got != "Argentina" {
			t.Errorf("answer = %q, want Argentina", got)
		}
		if got := g.View("p2").Round.Filled["p1"]; got != 1 {
			t.Errorf("filled = %d, want 1", got)
		}
		// Invisible padding does not count against the length limit.
		apply(t, g, now, "p1", SetAnswer{Category: 2, Text: strings.Repeat("a\u200b", MaxAnswerLen)})
	})
	t.Run("length limit counts runes", func(t *testing.T) {
		g := newPlaying(t, 2, nil)
		apply(t, g, now, "p1", SetAnswer{Category: 0, Text: strings.Repeat("ñ", MaxAnswerLen)})
		expectErr(t, g, now, "p1", SetAnswer{Category: 0, Text: strings.Repeat("ñ", MaxAnswerLen+1)}, ErrAnswerTooLong)
		// Padding does not count, it is trimmed first.
		apply(t, g, now, "p1", SetAnswer{Category: 0, Text: "  " + strings.Repeat("a", MaxAnswerLen) + "  "})
	})
	t.Run("category must exist", func(t *testing.T) {
		g := newPlaying(t, 2, nil)
		expectErr(t, g, now, "p1", SetAnswer{Category: -1, Text: "x"}, ErrInvalidCategory)
		expectErr(t, g, now, "p1", SetAnswer{Category: 6, Text: "x"}, ErrInvalidCategory)
		apply(t, g, now, "p1", SetAnswer{Category: 5, Text: "x"})
	})
	t.Run("only while playing or in grace", func(t *testing.T) {
		g := newLobby(t, 2)
		expectErr(t, g, now, "p1", SetAnswer{Category: 0, Text: "x"}, ErrWrongPhase)
		apply(t, g, tStart, "p1", StartGame{})
		expectErr(t, g, now, "p1", SetAnswer{Category: 0, Text: "x"}, ErrWrongPhase)
	})
}

func TestBasta(t *testing.T) {
	complete := []string{"Ana", "Argentina", "Alce"}
	tests := []struct {
		name    string
		answers []string // p1's answers; padded with empty strings
		want    error
	}{
		{"all valid", complete, nil},
		{"a leading zero width space is invisible", []string{"\u200bAna", "Argentina", "Alce"}, nil},
		{"an answer of only invisible characters is empty", []string{"Ana", "\u200b", "Alce"}, ErrBastaNotAllowed},
		{"accent and case are fine", []string{"ana", "ÁLAVA", "alce"}, nil},
		{"one empty", []string{"Ana", "", "Alce"}, ErrBastaNotAllowed},
		{"one blank", []string{"Ana", "   ", "Alce"}, ErrBastaNotAllowed},
		{"none filled", nil, ErrBastaNotAllowed},
		{"wrong letter", []string{"Ana", "Brasil", "Alce"}, ErrBastaNotAllowed},
		{"missing last", []string{"Ana", "Argentina"}, ErrBastaNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newPlaying(t, 3, threeCategories)
			now := tStart.Add(time.Second)
			fill(t, g, now, "p1", tt.answers...)
			fill(t, g, now, "p2", complete...) // others being complete is irrelevant

			if tt.want != nil {
				expectErr(t, g, now, "p1", Basta{}, tt.want)
				return
			}
			events := apply(t, g, now, "p1", Basta{})
			if g.Phase != PhaseGrace || g.Round.CallerID != "p1" {
				t.Fatalf("phase %s caller %q", g.Phase, g.Round.CallerID)
			}
			if want := now.Add(GraceDuration); !g.Round.GraceEndsAt.Equal(want) {
				t.Errorf("GraceEndsAt = %v, want %v", g.Round.GraceEndsAt, want)
			}
			if e, ok := hasEvent(events, EventBasta); !ok || e.PlayerID != "p1" || e.Name != "Player 1" {
				t.Errorf("events = %+v", events)
			}
		})
	}

	t.Run("wrong phase", func(t *testing.T) {
		g := newLobby(t, 2)
		expectErr(t, g, tStart, "p1", Basta{}, ErrWrongPhase)
		apply(t, g, tStart, "p1", StartGame{})
		expectErr(t, g, tStart, "p1", Basta{}, ErrWrongPhase)
	})
	t.Run("a second basta is refused", func(t *testing.T) {
		g := newPlaying(t, 2, threeCategories)
		now := tStart.Add(time.Second)
		fill(t, g, now, "p1", complete...)
		fill(t, g, now, "p2", complete...)
		apply(t, g, now, "p1", Basta{})
		expectErr(t, g, now.Add(time.Second), "p2", Basta{}, ErrWrongPhase)
	})
}

func TestGraceAndLockTiming(t *testing.T) {
	g := newPlaying(t, 2, threeCategories)
	callAt := tStart.Add(10 * time.Second)
	fill(t, g, callAt, "p1", "Ana", "Argentina", "Alce")
	fill(t, g, callAt, "p2", "Ana", "", "")
	apply(t, g, callAt, "p1", Basta{})

	graceEnd := callAt.Add(GraceDuration)
	lockEnd := graceEnd.Add(LateUpdateAllowance)
	if d, ok := g.NextDeadline(); !ok || !d.Equal(lockEnd) {
		t.Fatalf("NextDeadline = %v, %v, want %v", d, ok, lockEnd)
	}

	// During the 3 s countdown and up to the end of the allowance, answers are accepted.
	for name, at := range map[string]time.Time{
		"right after basta":         callAt,
		"during the countdown":      callAt.Add(time.Second),
		"1ms before the grace ends": graceEnd.Add(-ms(1)),
		"at the lock instant":       graceEnd,
		"1ms after the lock":        graceEnd.Add(ms(1)),
		"499ms after the lock":      graceEnd.Add(ms(499)),
		"1ns before the allowance":  lockEnd.Add(-time.Nanosecond),
	} {
		apply(t, g, at, "p2", SetAnswer{Category: 1, Text: "Alemania"})
		if g.Phase != PhaseGrace {
			t.Fatalf("%s: phase = %s, want grace", name, g.Phase)
		}
	}
	if events := g.Tick(lockEnd.Add(-time.Nanosecond)); len(events) != 0 {
		t.Fatalf("Tick before the allowance ended produced %v", events)
	}

	// At exactly lock + 500ms the round is locked.
	expectedBefore := g.Round.Answers["p2"][2]
	events, err := g.Apply(lockEnd, "p2", SetAnswer{Category: 2, Text: "Alce"})
	if err != ErrWrongPhase {
		t.Fatalf("late answer: err = %v, want ErrWrongPhase", err)
	}
	if e, ok := hasEvent(events, EventRoundLocked); !ok || e.Round != 1 {
		t.Errorf("events = %+v, want round_locked", events)
	}
	if g.Phase != PhaseReview || g.Round.ReviewCategory != 0 {
		t.Errorf("phase = %s", g.Phase)
	}
	if got := g.Round.Answers["p2"][2]; got != expectedBefore {
		t.Errorf("a late answer was stored: %q", got)
	}
	if got := g.Round.Answers["p2"][1]; got != "Alemania" {
		t.Errorf("answers sent inside the allowance must be kept, got %q", got)
	}
	if _, ok := g.NextDeadline(); ok {
		t.Error("the review has no deadline")
	}
}

func TestTimeLimit(t *testing.T) {
	limit := 60 * time.Second
	newLimited := func(t *testing.T) *Game {
		return newPlaying(t, 2, func(s *Settings) { s.TimeLimitSeconds = 60; threeCategories(s) })
	}

	t.Run("deadline is set when the letter is revealed", func(t *testing.T) {
		g := newLimited(t)
		if want := tStart.Add(limit); !g.Round.PlayingEndsAt.Equal(want) {
			t.Fatalf("PlayingEndsAt = %v, want %v", g.Round.PlayingEndsAt, want)
		}
		if d, ok := g.NextDeadline(); !ok || !d.Equal(tStart.Add(limit)) {
			t.Errorf("NextDeadline = %v, %v", d, ok)
		}
	})
	t.Run("the limit starts when the chooser stops, not when the round starts", func(t *testing.T) {
		g := newLobby(t, 2)
		configure(t, g, func(s *Settings) { s.TimeLimitSeconds = 90 })
		apply(t, g, tStart, "p1", StartGame{})
		stop := tStart.Add(4 * time.Second)
		apply(t, g, stop, "p1", StopLetter{})
		if want := stop.Add(90 * time.Second); !g.Round.PlayingEndsAt.Equal(want) {
			t.Errorf("PlayingEndsAt = %v, want %v", g.Round.PlayingEndsAt, want)
		}
	})
	t.Run("expiry enters grace with no caller", func(t *testing.T) {
		g := newLimited(t)
		deadline := tStart.Add(limit)
		if events := g.Tick(deadline.Add(-time.Nanosecond)); len(events) != 0 || g.Phase != PhasePlaying {
			t.Fatalf("Tick just before: events %v phase %s", events, g.Phase)
		}
		events := g.Tick(deadline)
		if g.Phase != PhaseGrace || g.Round.CallerID != "" || !g.Round.GraceEndsAt.Equal(deadline) {
			t.Fatalf("phase %s caller %q graceEndsAt %v", g.Phase, g.Round.CallerID, g.Round.GraceEndsAt)
		}
		if got := eventTypes(events); len(got) != 1 || got[0] != EventTimeUp {
			t.Errorf("events = %v, want [time_up] (and no basta)", got)
		}
		if d, ok := g.NextDeadline(); !ok || !d.Equal(deadline.Add(LateUpdateAllowance)) {
			t.Errorf("NextDeadline = %v, %v, want the end of the allowance", d, ok)
		}
	})
	t.Run("late updates are accepted for the allowance, then the round locks", func(t *testing.T) {
		g := newLimited(t)
		deadline := tStart.Add(limit)
		apply(t, g, deadline, "p1", SetAnswer{Category: 0, Text: "Ana"}) // Apply ticks first: now in grace
		if g.Phase != PhaseGrace {
			t.Fatalf("phase = %s", g.Phase)
		}
		apply(t, g, deadline.Add(ms(499)), "p1", SetAnswer{Category: 1, Text: "Argentina"})
		if _, err := g.Apply(deadline.Add(LateUpdateAllowance), "p1", SetAnswer{Category: 2, Text: "Alce"}); err != ErrWrongPhase {
			t.Fatalf("err = %v, want ErrWrongPhase", err)
		}
		if g.Phase != PhaseReview {
			t.Errorf("phase = %s, want review", g.Phase)
		}
		assertStrings(t, "answers", g.Round.Answers["p1"], []string{"Ana", "Argentina", ""})
	})
	t.Run("basta after the limit is a wrong-phase call", func(t *testing.T) {
		g := newLimited(t)
		fill(t, g, tStart.Add(time.Second), "p1", "Ana", "Argentina", "Alce")
		events, err := g.Apply(tStart.Add(limit), "p1", Basta{})
		if err != ErrWrongPhase {
			t.Fatalf("err = %v, want ErrWrongPhase", err)
		}
		if got := eventTypes(events); len(got) != 1 || got[0] != EventTimeUp {
			t.Errorf("events = %v, want [time_up]", got)
		}
		if g.Round.CallerID != "" {
			t.Errorf("caller = %q", g.Round.CallerID)
		}
	})
	t.Run("basta before the limit replaces it with the grace deadline", func(t *testing.T) {
		g := newLimited(t)
		now := tStart.Add(5 * time.Second)
		fill(t, g, now, "p1", "Ana", "Argentina", "Alce")
		apply(t, g, now, "p1", Basta{})
		if g.Round.CallerID != "p1" {
			t.Fatalf("caller = %q", g.Round.CallerID)
		}
		if d, ok := g.NextDeadline(); !ok || !d.Equal(now.Add(GraceDuration+LateUpdateAllowance)) {
			t.Errorf("NextDeadline = %v, %v", d, ok)
		}
		if events := g.Tick(now.Add(time.Second)); len(events) != 0 || g.Phase != PhaseGrace {
			t.Errorf("events %v phase %s", events, g.Phase)
		}
	})
	t.Run("a very late tick runs both transitions", func(t *testing.T) {
		g := newLimited(t)
		events := g.Tick(tStart.Add(time.Hour))
		got := eventTypes(events)
		if len(got) != 2 || got[0] != EventTimeUp || got[1] != EventRoundLocked {
			t.Errorf("events = %v", got)
		}
		if g.Phase != PhaseReview {
			t.Errorf("phase = %s", g.Phase)
		}
	})
}

func TestNextDeadlineByPhase(t *testing.T) {
	g := newLobby(t, 2)
	configure(t, g, func(s *Settings) { s.TimeLimitSeconds = 120; threeCategories(s) })
	check := func(phase Phase, want time.Time, wantOK bool) {
		t.Helper()
		if g.Phase != phase {
			t.Fatalf("phase = %s, want %s", g.Phase, phase)
		}
		got, ok := g.NextDeadline()
		if ok != wantOK || !got.Equal(want) {
			t.Errorf("%s: NextDeadline = %v, %v; want %v, %v", phase, got, ok, want, wantOK)
		}
	}

	check(PhaseLobby, time.Time{}, false)

	apply(t, g, tStart, "p1", StartGame{})
	check(PhaseLetter, tStart.Add(ChooserTimeout), true)

	stop := tStart.Add(ms(100)) // still the first letter, A
	apply(t, g, stop, "p1", StopLetter{})
	check(PhasePlaying, stop.Add(120*time.Second), true)

	now := stop.Add(10 * time.Second)
	fill(t, g, now, "p1", "Ana", "Argentina", "Alce")
	apply(t, g, now, "p1", Basta{})
	check(PhaseGrace, now.Add(GraceDuration+LateUpdateAllowance), true)

	review := now.Add(GraceDuration + LateUpdateAllowance)
	g.Tick(review)
	check(PhaseReview, time.Time{}, false)

	apply(t, g, review, "p1", FinishReview{})
	check(PhaseRoundSummary, time.Time{}, false)

	apply(t, g, review, "p1", EndGame{})
	check(PhaseFinal, time.Time{}, false)
}

func TestBastaRefusedOnCorruptAnswerSlots(t *testing.T) {
	// A snapshot with fewer answer slots than categories must not let basta through.
	g := newPlaying(t, 2, threeCategories)
	g.Round.Answers["p1"] = []string{"Ana", "Argentina"}
	expectErr(t, g, tStart.Add(time.Second), "p1", Basta{}, ErrBastaNotAllowed)
}

func TestLateAutoStopTickDatesTheRoundFromTheChooserDeadline(t *testing.T) {
	limit := 60 * time.Second
	chooserDeadline := tStart.Add(ChooserTimeout)
	start := func(t *testing.T) *Game {
		g := newLobby(t, 2)
		configure(t, g, func(s *Settings) { s.TimeLimitSeconds = 60; threeCategories(s) })
		apply(t, g, tStart, "p1", StartGame{})
		return g
	}

	t.Run("a tick 20 s late still starts the time limit at the chooser deadline", func(t *testing.T) {
		g := start(t)
		events := g.Tick(chooserDeadline.Add(20 * time.Second))
		if got := eventTypes(events); len(got) != 1 || got[0] != EventLetterRevealed {
			t.Fatalf("events = %v", got)
		}
		if g.Phase != PhasePlaying || g.Round.Letter != "D" {
			t.Fatalf("phase %s letter %q", g.Phase, g.Round.Letter)
		}
		want := chooserDeadline.Add(limit)
		if !g.Round.PlayingEndsAt.Equal(want) {
			t.Errorf("PlayingEndsAt = %v, want chooser deadline + 60 s = %v", g.Round.PlayingEndsAt, want)
		}
		if d, ok := g.NextDeadline(); !ok || !d.Equal(want) {
			t.Errorf("NextDeadline = %v, %v, want %v", d, ok, want)
		}
		if got := g.View("p1").Round.TimeLimitEndsAt; !got.Equal(want) {
			t.Errorf("TimeLimitEndsAt = %v, want %v", got, want)
		}
	})

	t.Run("Apply catches up the same way", func(t *testing.T) {
		g := start(t)
		apply(t, g, chooserDeadline.Add(20*time.Second), "p2", SetAnswer{Category: 0, Text: "Ana"})
		if want := chooserDeadline.Add(limit); !g.Round.PlayingEndsAt.Equal(want) {
			t.Errorf("PlayingEndsAt = %v, want %v", g.Round.PlayingEndsAt, want)
		}
	})

	t.Run("a tick after the whole time limit expires the round in the same call", func(t *testing.T) {
		g := start(t)
		events := g.Tick(chooserDeadline.Add(limit + LateUpdateAllowance))
		got := eventTypes(events)
		if len(got) != 3 || got[0] != EventLetterRevealed || got[1] != EventTimeUp || got[2] != EventRoundLocked {
			t.Fatalf("events = %v, want [letter_revealed time_up round_locked]", got)
		}
		if g.Phase != PhaseReview || g.Round.CallerID != "" {
			t.Errorf("phase %s caller %q", g.Phase, g.Round.CallerID)
		}
		if !g.Round.GraceEndsAt.Equal(chooserDeadline.Add(limit)) {
			t.Errorf("GraceEndsAt = %v", g.Round.GraceEndsAt)
		}
		assertStrings(t, "UsedLetters", g.UsedLetters, []string{"D"})
	})

	t.Run("catch-up stops at the first deadline that is still in the future", func(t *testing.T) {
		g := start(t)
		// Past the time limit but not past the allowance: the round is in grace.
		events := g.Tick(chooserDeadline.Add(limit + ms(100)))
		got := eventTypes(events)
		if len(got) != 2 || got[0] != EventLetterRevealed || got[1] != EventTimeUp {
			t.Fatalf("events = %v, want [letter_revealed time_up]", got)
		}
		if g.Phase != PhaseGrace {
			t.Fatalf("phase = %s, want grace", g.Phase)
		}
		if d, ok := g.NextDeadline(); !ok || !d.Equal(chooserDeadline.Add(limit+LateUpdateAllowance)) {
			t.Errorf("NextDeadline = %v, %v", d, ok)
		}
		// One nanosecond before the time limit the round is still playing.
		g = start(t)
		g.Tick(chooserDeadline.Add(limit - time.Nanosecond))
		if g.Phase != PhasePlaying {
			t.Errorf("phase = %s, want playing", g.Phase)
		}
	})

	t.Run("without a time limit the late tick only reveals the letter", func(t *testing.T) {
		g := newLobby(t, 2)
		apply(t, g, tStart, "p1", StartGame{})
		g.Tick(chooserDeadline.Add(time.Hour))
		if g.Phase != PhasePlaying || !g.Round.PlayingEndsAt.IsZero() {
			t.Errorf("phase %s PlayingEndsAt %v", g.Phase, g.Round.PlayingEndsAt)
		}
	})

	t.Run("a manual stop uses the time the command was received", func(t *testing.T) {
		g := start(t)
		stop := tStart.Add(7 * time.Second)
		apply(t, g, stop, "p1", StopLetter{})
		if want := stop.Add(limit); !g.Round.PlayingEndsAt.Equal(want) {
			t.Errorf("PlayingEndsAt = %v, want %v", g.Round.PlayingEndsAt, want)
		}
	})
}
