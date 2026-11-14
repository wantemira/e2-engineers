package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-buddy-training/orders/internal/orders"
)

func TestTrainingHTTP(t *testing.T) {
	handler := New(orders.NewService(&orders.MemoryRepository{}))
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/healthz", "", http.StatusOK},
		{"POST", "/orders", `{"product":"book","quantity":2}`, http.StatusCreated},
		{"POST", "/orders", `{"product":"","quantity":2}`, http.StatusBadRequest},
		{"POST", "/orders", `{"product":"book","quantity":0}`, http.StatusBadRequest},
		{"POST", "/orders", `{"product":"book","quantity":1} {}`, http.StatusBadRequest},
		{"POST", "/orders", `{"unknown":true}`, http.StatusBadRequest},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if response.Code != tc.status {
			t.Errorf("%s %s returned %d, want %d", tc.method, tc.path, response.Code, tc.status)
		}
		if response.Code == http.StatusCreated {
			var order orders.Order
			if err := json.Unmarshal(response.Body.Bytes(), &order); err != nil || order.ID != 1 || order.Quantity != 2 {
				t.Fatalf("unexpected order: %+v, %v", order, err)
			}
		}
	}
}
