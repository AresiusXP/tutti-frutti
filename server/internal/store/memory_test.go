package store

import (
	"testing"
	"time"
)

func TestMemoryContract(t *testing.T) {
	testStoreContract(t, func(t *testing.T) Store {
		s := NewMemory()
		t.Cleanup(s.Close)
		return s
	})
}

func TestMemoryClock(t *testing.T) {
	tests := []struct {
		name  string
		clock func() time.Time
		saved time.Time
		want  func(t *testing.T, got time.Time)
	}{
		{
			name:  "injected clock stamps zero UpdatedAt",
			clock: func() time.Time { return baseTime.Add(42 * time.Second) },
			want: func(t *testing.T, got time.Time) {
				if want := baseTime.Add(42 * time.Second); !got.Equal(want) {
					t.Errorf("UpdatedAt = %v, want %v", got, want)
				}
			},
		},
		{
			name:  "injected clock is normalised to UTC microseconds",
			clock: func() time.Time { return baseTime.In(time.FixedZone("X", 3600)).Add(999 * time.Nanosecond) },
			want: func(t *testing.T, got time.Time) {
				if !got.Equal(baseTime) || got.Location() != time.UTC {
					t.Errorf("UpdatedAt = %v (%v), want %v UTC", got, got.Location(), baseTime)
				}
			},
		},
		{
			name:  "explicit UpdatedAt bypasses the clock",
			clock: func() time.Time { t.Error("clock must not be called"); return time.Time{} },
			saved: baseTime,
			want: func(t *testing.T, got time.Time) {
				if !got.Equal(baseTime) {
					t.Errorf("UpdatedAt = %v, want %v", got, baseTime)
				}
			},
		},
		{
			name:  "nil clock falls back to the wall clock",
			clock: nil,
			want: func(t *testing.T, got time.Time) {
				if d := time.Since(got); d < -time.Second || d > time.Minute {
					t.Errorf("UpdatedAt = %v is not close to now", got)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMemoryWithClock(tc.clock)
			mustSave(t, m, snap("CK01", `{}`, tc.saved))
			got := mustLoadAll(t, m)
			if len(got) != 1 {
				t.Fatalf("LoadAll returned %d snapshots", len(got))
			}
			tc.want(t, got[0].UpdatedAt)
		})
	}
}

func TestMemoryClockAdvancesBetweenSaves(t *testing.T) {
	now := baseTime
	m := NewMemoryWithClock(func() time.Time { return now })
	mustSave(t, m, snap("A", `{}`, time.Time{}))
	now = now.Add(time.Hour)
	mustSave(t, m, snap("B", `{}`, time.Time{}))

	n, err := m.DeleteOlderThan(t.Context(), baseTime.Add(time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("DeleteOlderThan = (%d, %v), want (1, nil)", n, err)
	}
	if got := codesOf(mustLoadAll(t, m)); len(got) != 1 || got[0] != "B" {
		t.Errorf("remaining = %v, want [B]", got)
	}
}

func TestMemoryStaysUsableAfterClose(t *testing.T) {
	m := NewMemory()
	m.Close()
	mustSave(t, m, snap("AF01", `{}`, baseTime))
	if got := mustLoadAll(t, m); len(got) != 1 {
		t.Errorf("LoadAll returned %d snapshots", len(got))
	}
}
