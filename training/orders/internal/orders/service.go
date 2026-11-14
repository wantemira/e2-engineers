//go:build !solution

package orders

import (
	"context"
	"errors"
)

// Create is the exercise from PR #17. Preserve the repository error's cause.
func (service *Service) Create(ctx context.Context, input Input) (Order, error) {
	order, err := service.repository.Create(ctx, input)
	if err != nil {
		return Order{}, errors.New("create order failed")
	}
	return order, nil
}
