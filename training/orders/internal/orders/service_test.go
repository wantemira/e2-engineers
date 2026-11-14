package orders

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeRepository struct {
	err      error
	received Input
}

func (repository *fakeRepository) Create(_ context.Context, input Input) (Order, error) {
	repository.received = input
	return Order{ID: 17, Product: input.Product, Quantity: input.Quantity}, repository.err
}

func TestCreateRepositoryError(t *testing.T) {
	expected := errors.New("storage unavailable")
	service := NewService(&fakeRepository{err: expected})
	_, actual := service.Create(context.Background(), Input{Product: "book", Quantity: 1})
	if !errors.Is(actual, expected) {
		t.Fatalf("expected wrapped storage error, got %v", actual)
	}
}

func TestCreateSuccess(t *testing.T) {
	repository := &fakeRepository{}
	input := Input{Product: "book", Quantity: 2}
	order, err := NewService(repository).Create(context.Background(), input)
	if err != nil || repository.received != input || order.ID != 17 || order.Quantity != 2 {
		t.Fatalf("create returned %+v, %v; repository received %+v", order, err, repository.received)
	}
}

func TestMemoryRepositoryCancellation(t *testing.T) {
	repository := &MemoryRepository{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := repository.Create(ctx, Input{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	order, err := repository.Create(context.Background(), Input{Product: "book", Quantity: 1})
	if err != nil || order.ID != 1 {
		t.Fatalf("canceled call consumed an ID: %+v, %v", order, err)
	}
}

func TestMemoryRepositoryConcurrentCreate(t *testing.T) {
	const count = 32
	repository := &MemoryRepository{}
	ids := make(chan int, count)
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Go(func() {
			order, err := repository.Create(context.Background(), Input{Product: "book", Quantity: 1})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- order.ID
		})
	}
	workers.Wait()
	close(ids)
	seen := map[int]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("duplicate order ID %d", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("created %d unique orders, want %d", len(seen), count)
	}
}
