package store

import (
	"context"
	"testing"
	"time"
)

func TestCritiqueRepo_InsertAndList(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewCritiqueRepo(pool)

	now := time.Now().UTC()
	row := CritiqueRow{
		WindowStart: now.Add(-7 * 24 * time.Hour),
		WindowEnd:   now,
		SampleSize:  42,
		Model:       "claude-haiku-4-5",
		PromptHash:  "abcd1234",
		Status:      "done",
	}
	id, err := repo.InsertWithPatterns(ctx, row, []CritiquePatternRow{
		{PatternKey: "p1", Title: "trend 高估做多", Suggestion: "扣 15 分", Confidence: "high"},
		{PatternKey: "p2", Title: "高 funding 误判", Suggestion: "扣 10 分", Confidence: "medium"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == 0 {
		t.Fatal("want non-zero id")
	}

	rows, err := repo.List(ctx, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].SampleSize != 42 {
		t.Fatalf("List got %+v", rows)
	}

	patterns, err := repo.PatternsByCritique(ctx, id)
	if err != nil {
		t.Fatalf("PatternsByCritique: %v", err)
	}
	if len(patterns) != 2 {
		t.Fatalf("want 2 patterns, got %d", len(patterns))
	}
	if patterns[0].PatternKey != "p1" || patterns[1].PatternKey != "p2" {
		t.Fatalf("pattern keys: %v %v", patterns[0].PatternKey, patterns[1].PatternKey)
	}
}

func TestCritiqueRepo_PinUnpin(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewCritiqueRepo(pool)

	now := time.Now().UTC()
	id, err := repo.InsertWithPatterns(ctx, CritiqueRow{
		WindowStart: now, WindowEnd: now, SampleSize: 30,
		Model: "m", PromptHash: "h", Status: "done",
	}, []CritiquePatternRow{
		{PatternKey: "p1", Title: "t1", Suggestion: "s1", Confidence: "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	patterns, _ := repo.PatternsByCritique(ctx, id)
	if len(patterns) != 1 {
		t.Fatalf("want 1, got %d", len(patterns))
	}
	pid := patterns[0].ID

	pinned, _ := repo.PinnedPatterns(ctx, 10)
	if len(pinned) != 0 {
		t.Fatalf("expected no pinned initially, got %d", len(pinned))
	}
	if err := repo.SetPinned(ctx, pid, true, "manual"); err != nil {
		t.Fatal(err)
	}
	pinned, _ = repo.PinnedPatterns(ctx, 10)
	if len(pinned) != 1 || pinned[0].PinnedBy == nil || *pinned[0].PinnedBy != "manual" {
		t.Fatalf("after pin: %+v", pinned)
	}
	if err := repo.SetPinned(ctx, pid, false, ""); err != nil {
		t.Fatal(err)
	}
	pinned, _ = repo.PinnedPatterns(ctx, 10)
	if len(pinned) != 0 {
		t.Fatalf("after unpin: still %d pinned", len(pinned))
	}
}

func TestCritiqueRepo_CascadeDelete(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewCritiqueRepo(pool)
	now := time.Now().UTC()
	id, err := repo.InsertWithPatterns(ctx, CritiqueRow{
		WindowStart: now, WindowEnd: now, SampleSize: 1, Model: "m", PromptHash: "h", Status: "done",
	}, []CritiquePatternRow{{PatternKey: "p1", Title: "t", Suggestion: "s", Confidence: "low"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM agent_critiques WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_critique_patterns WHERE critique_id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("cascade failed: %d patterns remain", n)
	}
}
