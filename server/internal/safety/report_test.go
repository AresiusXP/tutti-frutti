package safety

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func intp(n int) *int { return &n }

func TestPurgeAt(t *testing.T) {
	created := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	tests := []struct {
		name     string
		resolved time.Time
		want     time.Time
	}{
		{"open report: 90 days after creation", time.Time{}, created.Add(90 * day)},
		{"resolved early: 30 days after resolution", created.Add(2 * day), created.Add(32 * day)},
		{"resolved late: never past 90 days after creation", created.Add(80 * day), created.Add(90 * day)},
		{"resolved exactly at the 60 day mark", created.Add(60 * day), created.Add(90 * day)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PurgeAt(created, tc.resolved); !got.Equal(tc.want) {
				t.Errorf("PurgeAt = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExpiredIsInclusive(t *testing.T) {
	at := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if Expired(at, at.Add(-time.Nanosecond)) || !Expired(at, at) || !Expired(at, at.Add(time.Hour)) {
		t.Error("a report is due exactly at its purge time, and not before")
	}
}

func TestVocabulary(t *testing.T) {
	for _, k := range Kinds {
		if !ValidKind(k) {
			t.Errorf("kind %q invalid", k)
		}
	}
	for _, r := range Reasons {
		if !ValidReason(r) {
			t.Errorf("reason %q invalid", r)
		}
	}
	for _, s := range Statuses {
		if !ValidStatus(s) {
			t.Errorf("status %q invalid", s)
		}
	}
	for _, bad := range []string{"", "Player", "player ", "spam2", "drop table"} {
		if ValidKind(bad) || ValidReason(bad) || ValidStatus(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if len(Kinds) != 4 || len(Reasons) != 8 {
		t.Errorf("kinds %d, reasons %d", len(Kinds), len(Reasons))
	}
}

func TestValidToken(t *testing.T) {
	good := []string{"a", "AbC-_9", "bot:xyz", strings.Repeat("a", MaxIDLen), "8f14e45f-ceea-467a-9575-6f5b5ba2ae1d"}
	bad := []string{"", strings.Repeat("a", MaxIDLen+1), "a b", "a/b", "é", "a\x00b", "a\nb", "a;b", `a"b`}
	for _, s := range good {
		if !ValidToken(s) {
			t.Errorf("%q rejected", s)
		}
	}
	for _, s := range bad {
		if ValidToken(s) {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestRefValidate(t *testing.T) {
	tests := []struct {
		name string
		kind string
		ref  Ref
		ok   bool
	}{
		{"player", KindPlayer, Ref{PlayerID: "p1"}, true},
		{"player without id", KindPlayer, Ref{}, false},
		{"player with a bad id", KindPlayer, Ref{PlayerID: "a b"}, false},
		{"label", KindCustomLabel, Ref{Category: intp(2), Label: "Cosas de gatos"}, true},
		{"label at index 0", KindCustomLabel, Ref{Category: intp(0), Label: "x"}, true},
		{"label without text", KindCustomLabel, Ref{Category: intp(2)}, false},
		{"label without index", KindCustomLabel, Ref{Label: "x"}, false},
		{"label index out of range", KindCustomLabel, Ref{Category: intp(10), Label: "x"}, false},
		{"negative index", KindCustomLabel, Ref{Category: intp(-1), Label: "x"}, false},
		{"huge label", KindCustomLabel, Ref{Category: intp(1), Label: strings.Repeat("x", 200)}, false},
		{"answer", KindAnswer, Ref{PlayerID: "p1", Category: intp(1), Round: intp(2)}, true},
		{"answer without round", KindAnswer, Ref{PlayerID: "p1", Category: intp(1)}, false},
		{"answer round 0", KindAnswer, Ref{PlayerID: "p1", Category: intp(1), Round: intp(0)}, false},
		{"answer without player", KindAnswer, Ref{Category: intp(1), Round: intp(1)}, false},
		{"verdict", KindAIVerdict, Ref{PlayerID: "p1", Category: intp(0), Round: intp(1)}, true},
		{"verdict without player", KindAIVerdict, Ref{Category: intp(0), Round: intp(1)}, false},
		{"unknown kind", "video", Ref{PlayerID: "p1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.ref.Validate(tc.kind); (err == nil) != tc.ok {
				t.Errorf("Validate = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestContentKey(t *testing.T) {
	a := ContentKey(KindAnswer, Ref{PlayerID: "p1", Category: intp(1), Round: intp(2)})
	if a != "answer:r2:c1:p1" {
		t.Errorf("answer key = %q", a)
	}
	if v := ContentKey(KindAIVerdict, Ref{PlayerID: "p1", Category: intp(1), Round: intp(2)}); v != "verdict:r2:c1:p1" {
		t.Errorf("verdict key = %q", v)
	}
	if p := ContentKey(KindPlayer, Ref{PlayerID: "p1"}); p != "player:p1" {
		t.Errorf("player key = %q", p)
	}
	l1 := ContentKey(KindCustomLabel, Ref{Category: intp(3), Label: "Cosas de gatos"})
	l2 := ContentKey(KindCustomLabel, Ref{Category: intp(3), Label: "Cosas de perros"})
	if l1 == l2 || !strings.HasPrefix(l1, "label:3:") {
		t.Errorf("label keys %q and %q: a changed label must be another key", l1, l2)
	}
	if l1 != ContentKey(KindCustomLabel, Ref{Category: intp(3), Label: "Cosas de gatos"}) {
		t.Error("the key is not stable")
	}
}

// Evidence is JSON for a moderator: whatever a player typed must survive as
// text and the document must stay valid JSON (invalid UTF-8, control characters,
// quotes, markup, NUL).
func TestEvidenceJSONIsSafe(t *testing.T) {
	nasty := "</script><img src=x onerror=alert(1)> \"quote\" \\ back\\u0000slash \u2028 \xff\xfe \x00 \x07 emoji 👨‍💻 ñ"
	ev := Evidence{Verified: true, Data: EvidenceData{
		Source: SourceServer, Text: nasty,
		Player:  &EvidencePlayer{ID: "p1", Name: nasty, Emoji: "🍎", Color: "#e11"},
		Claimed: &Ref{Label: nasty}, ObservedText: nasty,
	}}
	raw, err := ev.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("not valid JSON: %q", raw)
	}
	if strings.ContainsRune(string(raw), 0) || strings.ContainsAny(string(raw), "\x07\u2028") {
		t.Errorf("raw control characters survived in %q", raw)
	}
	var back EvidenceData
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	// Valid text round-trips unchanged; only bytes that are not UTF-8 are replaced.
	want := string([]rune(nasty))
	if back.Text != want || back.ObservedText != want || back.Player.Name != want {
		t.Errorf("text changed: %q, want %q", back.Text, want)
	}
	if !strings.Contains(back.Text, "</script>") || !strings.Contains(back.Text, `\u0000`) {
		t.Error("the literal text a player typed must be stored as typed")
	}
}
