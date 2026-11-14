package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"ai-buddy-training/orders/internal/orders"
)

func New(service *orders.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{\"status\":\"ok\",\"service\":\"orders-training\"}\n")
	})
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		var input orders.Input
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid order", http.StatusBadRequest)
			return
		}
		if decoder.Decode(new(any)) != io.EOF || strings.TrimSpace(input.Product) == "" || input.Quantity < 1 {
			http.Error(w, "invalid order", http.StatusBadRequest)
			return
		}
		order, err := service.Create(r.Context(), input)
		if err != nil {
			http.Error(w, "could not create order", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(order)
	})
	return mux
}
