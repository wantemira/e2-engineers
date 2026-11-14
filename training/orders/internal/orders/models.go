package orders

import "context"

type Input struct {
	Product  string `json:"product"`
	Quantity int    `json:"quantity"`
}

type Order struct {
	ID       int    `json:"id"`
	Product  string `json:"product"`
	Quantity int    `json:"quantity"`
}

type Repository interface {
	Create(context.Context, Input) (Order, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}
