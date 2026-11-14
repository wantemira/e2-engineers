//go:build solution

package orders

import (
	"context"
	"fmt"
)

func (service *Service) Create(ctx context.Context, input Input) (Order, error) {
	order, err := service.repository.Create(ctx, input)
	if err != nil {
		return Order{}, fmt.Errorf("create order: %w", err)
	}
	return order, nil
}
