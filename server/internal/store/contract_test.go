package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"
)

// baseTime is a fixed instant with a sub-second part that survives timestamptz
// (microsecond) precision.
var baseTime = time.Date(2026, time.March, 14, 9, 26, 53, 589793000, time.UTC)

func snap(code, state string, at time.Time) Snapshot {
	return Snapshot{Code: code, State: json.RawMessage(state), UpdatedAt: at}
}

func jsonEqual(t *testing.T, want, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("want is not JSON: %v (%s)", err, want)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got is not JSON: %v (%s)", err, got)
	}
	if !reflect.DeepEqual(w, g) {
		t.Errorf("JSON differs:\n want %s\n  got %s", want, got)
	}
}

func mustSave(t *testing.T, s Store, snaps ...Snapshot) {
	t.Helper()
	for _, sn := range snaps {
		if err := s.Save(t.Context(), sn); err != nil {
			t.Fatalf("Save(%q): %v", sn.Code, err)
		}
	}
}

func mustLoadAll(t *testing.T, s Store) []Snapshot {
	t.Helper()
	got, err := s.LoadAll(t.Context())
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return got
}

func codesOf(snaps []Snapshot) []string {
	codes := make([]string, len(snaps))
	for i, s := range snaps {
		codes[i] = s.Code
	}
	return codes
}

// testStoreContract is the behaviour every Store implementation must show.
// newStore must return an empty store that is closed when the test ends.
func testStoreContract(t *testing.T, newStore func(t *testing.T) Store) {
	t.Helper()

	t.Run("save/load round trip", func(t *testing.T) {
		payloads := []struct{ name, state string }{
			{"empty object", `{}`},
			{"flat", `{"phase":"lobby","round":0}`},
			{"nested", `{"game":{"players":[{"id":"a","score":10,"answers":{"name":"Ana"}},{"id":"b","score":0,"answers":null}],"settings":{"rounds":5,"categories":["fruta","país"]}},"version":1}`},
			{"unicode", `{"names":["Ñandú","日本語","🍉🍎","Zoë","\u00e9\ud83c\udf4e"]}`},
			{"escapes", `{"s":"quote \" backslash \\ newline \n tab \t slash \/ literal \\u0000"}`},
			{"numbers", `{"i":-42,"f":3.14159,"big":9007199254740991,"zero":0}`},
			{"array", `[1,2,3]`},
			{"scalar", `"just a string"`},
			{"null literal", `null`},
			{"whitespace", "  {\n  \"a\" : 1 }\n"},
		}
		for _, tc := range payloads {
			t.Run(tc.name, func(t *testing.T) {
				s := newStore(t)
				mustSave(t, s, snap("RT01", tc.state, baseTime))
				got := mustLoadAll(t, s)
				if len(got) != 1 {
					t.Fatalf("LoadAll returned %d snapshots, want 1", len(got))
				}
				if got[0].Code != "RT01" {
					t.Errorf("Code = %q", got[0].Code)
				}
				jsonEqual(t, []byte(tc.state), got[0].State)
				if !got[0].UpdatedAt.Equal(baseTime) {
					t.Errorf("UpdatedAt = %v, want %v", got[0].UpdatedAt, baseTime)
				}
				if got[0].UpdatedAt.Location() != time.UTC {
					t.Errorf("UpdatedAt location = %v, want UTC", got[0].UpdatedAt.Location())
				}
			})
		}
	})

	t.Run("upsert replaces state and bumps UpdatedAt", func(t *testing.T) {
		s := newStore(t)
		mustSave(t, s,
			snap("UP01", `{"v":1}`, baseTime),
			snap("OTHER", `{"v":"untouched"}`, baseTime),
		)
		later := baseTime.Add(5 * time.Second)
		mustSave(t, s, snap("UP01", `{"v":2,"extra":[true]}`, later))

		got := mustLoadAll(t, s)
		if want := []string{"OTHER", "UP01"}; !slices.Equal(codesOf(got), want) {
			t.Fatalf("codes = %v, want %v", codesOf(got), want)
		}
		jsonEqual(t, []byte(`{"v":2,"extra":[true]}`), got[1].State)
		if !got[1].UpdatedAt.Equal(later) {
			t.Errorf("UpdatedAt = %v, want %v", got[1].UpdatedAt, later)
		}
		jsonEqual(t, []byte(`{"v":"untouched"}`), got[0].State)
		if !got[0].UpdatedAt.Equal(baseTime) {
			t.Errorf("other snapshot UpdatedAt changed to %v", got[0].UpdatedAt)
		}
	})

	t.Run("UpdatedAt", func(t *testing.T) {
		tests := []struct {
			name  string
			at    time.Time
			check func(t *testing.T, got, before, after time.Time)
		}{
			{
				name: "zero is stamped with the clock",
				check: func(t *testing.T, got, before, after time.Time) {
					if got.Before(before) || got.After(after) {
						t.Errorf("UpdatedAt = %v, want within [%v, %v]", got, before, after)
					}
				},
			},
			{
				name: "explicit value is kept",
				at:   baseTime,
				check: func(t *testing.T, got, _, _ time.Time) {
					if !got.Equal(baseTime) {
						t.Errorf("UpdatedAt = %v, want %v", got, baseTime)
					}
				},
			},
			{
				name: "non-UTC zone is normalised to UTC",
				at:   baseTime.In(time.FixedZone("UTC+9", 9*3600)),
				check: func(t *testing.T, got, _, _ time.Time) {
					if !got.Equal(baseTime) || got.Location() != time.UTC {
						t.Errorf("UpdatedAt = %v (%v), want %v in UTC", got, got.Location(), baseTime)
					}
				},
			},
			{
				name: "sub-microsecond precision is truncated",
				at:   baseTime.Add(789 * time.Nanosecond),
				check: func(t *testing.T, got, _, _ time.Time) {
					if !got.Equal(baseTime) {
						t.Errorf("UpdatedAt = %v, want %v", got, baseTime)
					}
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				s := newStore(t)
				before := time.Now().UTC().Truncate(time.Microsecond)
				mustSave(t, s, snap("TS01", `{}`, tc.at))
				after := time.Now().UTC().Add(time.Microsecond)
				got := mustLoadAll(t, s)
				if len(got) != 1 {
					t.Fatalf("LoadAll returned %d snapshots", len(got))
				}
				tc.check(t, got[0].UpdatedAt, before, after)
			})
		}
	})

	t.Run("Delete", func(t *testing.T) {
		tests := []struct {
			name string
			code string
			want []string
		}{
			{"existing", "DL02", []string{"DL01", "DL03"}},
			{"unknown is not an error", "NOPE", []string{"DL01", "DL02", "DL03"}},
			{"empty code is not an error", "", []string{"DL01", "DL02", "DL03"}},
			{"codes are case sensitive", "dl01", []string{"DL01", "DL02", "DL03"}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				s := newStore(t)
				mustSave(t, s,
					snap("DL01", `{}`, baseTime),
					snap("DL02", `{}`, baseTime),
					snap("DL03", `{}`, baseTime),
				)
				if err := s.Delete(t.Context(), tc.code); err != nil {
					t.Fatalf("Delete(%q): %v", tc.code, err)
				}
				if got := codesOf(mustLoadAll(t, s)); !slices.Equal(got, tc.want) {
					t.Errorf("codes after delete = %v, want %v", got, tc.want)
				}
			})
		}

		t.Run("deleted code can be saved again", func(t *testing.T) {
			s := newStore(t)
			mustSave(t, s, snap("AG01", `{"v":1}`, baseTime))
			if err := s.Delete(t.Context(), "AG01"); err != nil {
				t.Fatal(err)
			}
			mustSave(t, s, snap("AG01", `{"v":2}`, baseTime))
			got := mustLoadAll(t, s)
			if len(got) != 1 {
				t.Fatalf("LoadAll returned %d snapshots", len(got))
			}
			jsonEqual(t, []byte(`{"v":2}`), got[0].State)
		})
	})

	t.Run("LoadAll", func(t *testing.T) {
		t.Run("empty store", func(t *testing.T) {
			s := newStore(t)
			got, err := s.LoadAll(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Errorf("LoadAll on empty store = %v", got)
			}
		})

		t.Run("sorted by code in byte order", func(t *testing.T) {
			// Inserted out of order; digits < upper case < lower case.
			insert := []string{"ZZZZ", "BCDF", "9999", "2345", "Wxyz", "a", "B", "BCD", "aB", "Ab", "~", "ÑAND"}
			s := newStore(t)
			for _, code := range insert {
				mustSave(t, s, snap(code, `{}`, baseTime))
			}
			want := slices.Clone(insert)
			sort.Strings(want)
			if got := codesOf(mustLoadAll(t, s)); !slices.Equal(got, want) {
				t.Errorf("order = %q, want %q", got, want)
			}
		})
	})

	t.Run("DeleteOlderThan", func(t *testing.T) {
		seed := func(t *testing.T) Store {
			s := newStore(t)
			mustSave(t, s,
				snap("OLD1", `{}`, baseTime.Add(-time.Hour)),
				snap("OLD2", `{}`, baseTime.Add(-time.Microsecond)),
				snap("EDGE", `{}`, baseTime),
				snap("NEW1", `{}`, baseTime.Add(time.Microsecond)),
				snap("NEW2", `{}`, baseTime.Add(time.Hour)),
			)
			return s
		}
		tests := []struct {
			name      string
			cutoff    time.Time
			wantN     int64
			wantCodes []string
		}{
			{"strictly before: exactly equal is kept", baseTime, 2, []string{"EDGE", "NEW1", "NEW2"}},
			{"one microsecond later drops the edge", baseTime.Add(time.Microsecond), 3, []string{"NEW1", "NEW2"}},
			{"older than everything", baseTime.Add(-24 * time.Hour), 0, []string{"EDGE", "NEW1", "NEW2", "OLD1", "OLD2"}},
			{"newer than everything", baseTime.Add(24 * time.Hour), 5, []string{}},
			{"zero time deletes nothing", time.Time{}, 0, []string{"EDGE", "NEW1", "NEW2", "OLD1", "OLD2"}},
			{"cutoff in another zone", baseTime.In(time.FixedZone("UTC-5", -5*3600)), 2, []string{"EDGE", "NEW1", "NEW2"}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				s := seed(t)
				n, err := s.DeleteOlderThan(t.Context(), tc.cutoff)
				if err != nil {
					t.Fatal(err)
				}
				if n != tc.wantN {
					t.Errorf("deleted %d, want %d", n, tc.wantN)
				}
				if got := codesOf(mustLoadAll(t, s)); !slices.Equal(got, tc.wantCodes) && !(len(got) == 0 && len(tc.wantCodes) == 0) {
					t.Errorf("remaining = %v, want %v", got, tc.wantCodes)
				}
			})
		}

		t.Run("second call is a no-op", func(t *testing.T) {
			s := seed(t)
			if n, err := s.DeleteOlderThan(t.Context(), baseTime); err != nil || n != 2 {
				t.Fatalf("first call = (%d, %v), want (2, nil)", n, err)
			}
			if n, err := s.DeleteOlderThan(t.Context(), baseTime); err != nil || n != 0 {
				t.Fatalf("second call = (%d, %v), want (0, nil)", n, err)
			}
		})

		t.Run("empty store", func(t *testing.T) {
			s := newStore(t)
			if n, err := s.DeleteOlderThan(t.Context(), baseTime); err != nil || n != 0 {
				t.Fatalf("= (%d, %v), want (0, nil)", n, err)
			}
		})

		t.Run("upsert refreshes the age", func(t *testing.T) {
			s := seed(t)
			mustSave(t, s, snap("OLD1", `{"fresh":true}`, baseTime.Add(2*time.Hour)))
			n, err := s.DeleteOlderThan(t.Context(), baseTime)
			if err != nil || n != 1 {
				t.Fatalf("= (%d, %v), want (1, nil)", n, err)
			}
		})
	})

	t.Run("Save rejects invalid snapshots", func(t *testing.T) {
		tests := []struct {
			name string
			snap Snapshot
		}{
			{"empty code", snap("", `{}`, baseTime)},
			{"code with NUL", snap("A\x00B", `{}`, baseTime)},
			{"code with invalid UTF-8", snap("A\xffB", `{}`, baseTime)},
			{"nil state", Snapshot{Code: "BAD1", UpdatedAt: baseTime}},
			{"empty state", snap("BAD1", ``, baseTime)},
			{"plain text", snap("BAD1", `not json`, baseTime)},
			{"truncated object", snap("BAD1", `{"a":`, baseTime)},
			{"missing value", snap("BAD1", `{"a":}`, baseTime)},
			{"trailing garbage", snap("BAD1", `{} {}`, baseTime)},
			{"single quotes", snap("BAD1", `{'a':1}`, baseTime)},
			{"invalid UTF-8 in string", snap("BAD1", "{\"a\":\"\xff\"}", baseTime)},
			{"NUL escape in value", snap("BAD1", `{"a":"x\u0000y"}`, baseTime)},
			{"NUL escape in key", snap("BAD1", `{"x\u0000y":1}`, baseTime)},
			{"NUL escape nested in array", snap("BAD1", `[{"a":["\u0000"]}]`, baseTime)},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				s := newStore(t)
				err := s.Save(t.Context(), tc.snap)
				if !errors.Is(err, ErrInvalidSnapshot) {
					t.Fatalf("Save error = %v, want ErrInvalidSnapshot", err)
				}
				if got := mustLoadAll(t, s); len(got) != 0 {
					t.Errorf("store holds %v after a rejected Save", codesOf(got))
				}
			})
		}

		t.Run("a rejected upsert keeps the previous state", func(t *testing.T) {
			s := newStore(t)
			mustSave(t, s, snap("KEEP", `{"v":1}`, baseTime))
			if err := s.Save(t.Context(), snap("KEEP", `{bad`, baseTime.Add(time.Hour))); !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("Save error = %v", err)
			}
			got := mustLoadAll(t, s)
			if len(got) != 1 {
				t.Fatalf("LoadAll returned %d snapshots", len(got))
			}
			jsonEqual(t, []byte(`{"v":1}`), got[0].State)
			if !got[0].UpdatedAt.Equal(baseTime) {
				t.Errorf("UpdatedAt = %v, want %v", got[0].UpdatedAt, baseTime)
			}
		})

		t.Run("an escaped backslash before u0000 is plain text and accepted", func(t *testing.T) {
			s := newStore(t)
			mustSave(t, s, snap("OKOK", `{"a":"\\u0000"}`, baseTime))
			got := mustLoadAll(t, s)
			if len(got) != 1 {
				t.Fatalf("LoadAll returned %d snapshots", len(got))
			}
			jsonEqual(t, []byte(`{"a":"\\u0000"}`), got[0].State)
		})
	})

	t.Run("cancelled context", func(t *testing.T) {
		calls := []struct {
			name string
			call func(ctx context.Context, s Store) error
		}{
			{"Save", func(ctx context.Context, s Store) error { return s.Save(ctx, snap("CX01", `{}`, baseTime)) }},
			{"Save invalid", func(ctx context.Context, s Store) error { return s.Save(ctx, snap("", `x`, baseTime)) }},
			{"Delete", func(ctx context.Context, s Store) error { return s.Delete(ctx, "SEED") }},
			{"LoadAll", func(ctx context.Context, s Store) error { _, err := s.LoadAll(ctx); return err }},
			{"DeleteOlderThan", func(ctx context.Context, s Store) error {
				_, err := s.DeleteOlderThan(ctx, baseTime.Add(time.Hour))
				return err
			}},
			{"Ping", func(ctx context.Context, s Store) error { return s.Ping(ctx) }},
		}
		for _, tc := range calls {
			t.Run(tc.name, func(t *testing.T) {
				s := newStore(t)
				mustSave(t, s, snap("SEED", `{"v":1}`, baseTime))
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := tc.call(ctx, s); !errors.Is(err, context.Canceled) {
					t.Errorf("error = %v, want context.Canceled", err)
				}
				got := mustLoadAll(t, s)
				if want := []string{"SEED"}; !slices.Equal(codesOf(got), want) {
					t.Errorf("store changed by a cancelled call: %v", codesOf(got))
				}
			})
		}

		t.Run("expired deadline", func(t *testing.T) {
			s := newStore(t)
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			defer cancel()
			if err := s.Ping(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("Ping error = %v, want context.DeadlineExceeded", err)
			}
		})
	})

	t.Run("no aliasing of State", func(t *testing.T) {
		const original = `{"k":"original"}`

		t.Run("mutating the saved slice", func(t *testing.T) {
			s := newStore(t)
			in := []byte(original)
			mustSave(t, s, Snapshot{Code: "AL01", State: in, UpdatedAt: baseTime})
			for i := range in {
				in[i] = 'x'
			}
			got := mustLoadAll(t, s)
			if len(got) != 1 {
				t.Fatalf("LoadAll returned %d snapshots", len(got))
			}
			// Semantic comparison: jsonb re-formats the document.
			jsonEqual(t, []byte(original), got[0].State)
		})

		t.Run("mutating a loaded slice", func(t *testing.T) {
			s := newStore(t)
			mustSave(t, s, snap("AL01", original, baseTime))
			first := mustLoadAll(t, s)
			for i := range first[0].State {
				first[0].State[i] = 'x'
			}
			first[0].Code = "MUTATED"
			second := mustLoadAll(t, s)
			if len(second) != 1 || second[0].Code != "AL01" {
				t.Fatalf("second load = %v", second)
			}
			jsonEqual(t, []byte(original), second[0].State)
		})

		t.Run("loads do not share backing arrays", func(t *testing.T) {
			s := newStore(t)
			mustSave(t, s, snap("AL01", original, baseTime), snap("AL02", original, baseTime))
			a, b := mustLoadAll(t, s), mustLoadAll(t, s)
			for _, sn := range a {
				for i := range sn.State {
					sn.State[i] = 'x'
				}
			}
			jsonEqual(t, []byte(original), b[0].State)
			jsonEqual(t, []byte(original), b[1].State)
		})
	})

	t.Run("Ping", func(t *testing.T) {
		s := newStore(t)
		if err := s.Ping(t.Context()); err != nil {
			t.Errorf("Ping: %v", err)
		}
	})

	t.Run("Close is idempotent", func(t *testing.T) {
		s := newStore(t)
		mustSave(t, s, snap("CL01", `{}`, baseTime))
		s.Close()
		s.Close()
	})

	t.Run("concurrent use", func(t *testing.T) {
		const (
			workers = 8
			perWork = 20
		)
		s := newStore(t)
		ctx := t.Context()
		var wg sync.WaitGroup
		for w := range workers {
			wg.Go(func() {
				for i := range perWork {
					code := fmt.Sprintf("W%dC%02d", w, i)
					state := fmt.Sprintf(`{"worker":%d,"i":%d}`, w, i)
					if err := s.Save(ctx, snap(code, state, baseTime.Add(time.Duration(i)*time.Second))); err != nil {
						t.Errorf("Save(%s): %v", code, err)
						return
					}
					if i%2 == 1 {
						if err := s.Delete(ctx, code); err != nil {
							t.Errorf("Delete(%s): %v", code, err)
							return
						}
					}
					if i%5 == 0 {
						all, err := s.LoadAll(ctx)
						if err != nil {
							t.Errorf("LoadAll: %v", err)
							return
						}
						if !sort.SliceIsSorted(all, func(a, b int) bool { return all[a].Code < all[b].Code }) {
							t.Errorf("LoadAll not sorted: %v", codesOf(all))
						}
						for _, sn := range all {
							if !json.Valid(sn.State) {
								t.Errorf("LoadAll returned invalid state for %s: %s", sn.Code, sn.State)
							}
						}
					}
					if i%7 == 0 {
						if _, err := s.DeleteOlderThan(ctx, baseTime.Add(-24*time.Hour)); err != nil {
							t.Errorf("DeleteOlderThan: %v", err)
							return
						}
						if err := s.Ping(ctx); err != nil {
							t.Errorf("Ping: %v", err)
							return
						}
					}
					// Everyone hammers the same code too (upsert contention).
					if err := s.Save(ctx, snap("SHARED", state, baseTime)); err != nil {
						t.Errorf("Save(SHARED): %v", err)
						return
					}
				}
			})
		}
		wg.Wait()

		got := mustLoadAll(t, s)
		// Even indexes survive (perWork/2 per worker) plus the shared code.
		if want := workers*(perWork/2) + 1; len(got) != want {
			t.Errorf("snapshots left = %d, want %d", len(got), want)
		}
	})
}
