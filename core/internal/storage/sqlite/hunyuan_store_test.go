package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

func TestHunyuanBudgetRaceAndIdempotency(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "hy.db"))
	defer store.Close()
	ctx := context.Background()
	if err := store.SetHunyuanBudget(ctx, contract.HunyuanDefaultProject, contract.HunyuanDefaultBudget, 1, 4); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var ok int
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := store.CreateHunyuanInvocation(ctx, HunyuanInvocationRecord{
				ID: fmt.Sprintf("hyinv_%d", i), ProjectID: contract.HunyuanDefaultProject,
				AccessTokenID: "access_token_x", RequestedModel: "m",
				InputHash: fmt.Sprintf("h%d", i), RequestHash: fmt.Sprintf("h%d", i),
				TraceID: fmt.Sprintf("t%d", i), BudgetPolicyRef: contract.HunyuanDefaultBudget,
			})
			if err == nil && result.Created {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("created=%d want 1", ok)
	}
}

func TestHunyuanTerminalRecovery(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "hy.db"))
	defer store.Close()
	ctx := context.Background()
	p, b := contract.HunyuanDefaultProject, contract.HunyuanDefaultBudget
	if err := store.SetHunyuanBudget(ctx, p, b, 10, 2); err != nil {
		t.Fatal(err)
	}
	create := func(id string) HunyuanInvocationRecord {
		t.Helper()
		result, err := store.CreateHunyuanInvocation(ctx, HunyuanInvocationRecord{ID: id, ProjectID: p, RequestedModel: "m", TraceID: id, BudgetPolicyRef: b})
		if err != nil {
			t.Fatal(err)
		}
		return result.Record
	}
	inflight := func(want int) {
		t.Helper()
		var got int
		if err := store.db.QueryRowContext(ctx, "SELECT inflight FROM hunyuan_budget WHERE project_id=? AND policy_ref=?", p, b).Scan(&got); err != nil || got != want {
			t.Fatalf("inflight=%d want=%d error=%v", got, want, err)
		}
	}
	active := create("active")
	active.Status = contract.HunyuanStatusDispatching
	if err := store.UpdateHunyuanInvocation(ctx, active); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHunyuanBudget(ctx, p, b, 10, 2); err != nil {
		t.Fatal(err)
	}
	inflight(1)
	if _, ok, err := store.CancelHunyuanInvocation(ctx, active.ID, p); err != nil || !ok {
		t.Fatalf("cancel=%t %v", ok, err)
	}
	inflight(1)
	now := store.now()
	active.FinishedAt = &now
	active.Status = contract.HunyuanStatusSucceeded
	active.OutputJSON = "{}"
	if err := store.UpdateHunyuanInvocation(ctx, active); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetHunyuanInvocation(ctx, active.ID, p)
	if err != nil || got.Status != contract.HunyuanStatusCanceled || got.OutputJSON != "" {
		t.Fatalf("late success=%+v %v", got, err)
	}
	if err := store.UpdateHunyuanInvocation(ctx, active); err != ErrHunyuanTransition {
		t.Fatalf("duplicate=%v", err)
	}
	inflight(0)
	reserved := create("reserved")
	dispatched := create("dispatched")
	dispatched.Status = contract.HunyuanStatusStreaming
	if err := store.UpdateHunyuanInvocation(ctx, dispatched); err != nil {
		t.Fatal(err)
	}
	if n, err := store.RecoverPendingHunyuanInvocations(ctx); err != nil || n != 2 {
		t.Fatalf("recovery=%d %v", n, err)
	}
	inflight(0)
	for id, want := range map[string]string{reserved.ID: contract.HunyuanStatusCanceled, dispatched.ID: contract.HunyuanStatusUnknown} {
		got, err := store.GetHunyuanInvocation(ctx, id, p)
		if err != nil || got.Status != want || got.FinishedAt == nil {
			t.Fatalf("recovered=%+v %v", got, err)
		}
	}
	if n, err := store.RecoverPendingHunyuanInvocations(ctx); err != nil || n != 0 {
		t.Fatalf("repeat=%d %v", n, err)
	}
}
