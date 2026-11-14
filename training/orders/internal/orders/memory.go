package orders

import (
	"context"
	"sync"
)

type MemoryRepository struct {
	mu     sync.Mutex
	nextID int
}

func (repository *MemoryRepository) Create(ctx context.Context, input Input) (Order, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Order{}, err
	}
	repository.nextID++
	return Order{ID: repository.nextID, Product: input.Product, Quantity: input.Quantity}, nil
}
