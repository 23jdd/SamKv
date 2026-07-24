package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestLogRouterWritesSingleAndBatchEntries(t *testing.T) {
	router := newTestRouter(t)
	single := performRequest(router, http.MethodPost, "/logs", `{
		"timestamp":"2024-01-01T10:00:00Z",
		"labels":{"app":"nginx","level":"ERROR"},
		"message":"upstream failed"
	}`)
	if single.Code != http.StatusCreated {
		t.Fatalf("single status=%d body=%s", single.Code, single.Body.String())
	}
	var singleResponse logWriteResponse
	if err := json.Unmarshal(single.Body.Bytes(), &singleResponse); err != nil {
		t.Fatal(err)
	}
	if singleResponse.Sequence == 0 {
		t.Fatal("single write returned zero sequence")
	}

	batch := performRequest(router, http.MethodPost, "/logs/batch", `{
		"entries":[
			{"labels":{"app":"api"},"message":"first"},
			{"labels":{"app":"api"},"message":"second"}
		]
	}`)
	if batch.Code != http.StatusCreated {
		t.Fatalf("batch status=%d body=%s", batch.Code, batch.Body.String())
	}
	var batchResponse logBatchWriteResponse
	if err := json.Unmarshal(batch.Body.Bytes(), &batchResponse); err != nil {
		t.Fatal(err)
	}
	if len(batchResponse.Sequences) != 2 || batchResponse.Sequences[0] == 0 ||
		batchResponse.Sequences[1] <= batchResponse.Sequences[0] {
		t.Fatalf("batch sequences = %#v", batchResponse.Sequences)
	}
}

func TestLogRouterRejectsInvalidWriteRequests(t *testing.T) {
	router := newTestRouter(t)
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "missing message", path: "/logs", body: `{"labels":{"app":"api"}}`},
		{name: "invalid timestamp", path: "/logs", body: `{"timestamp":"yesterday","message":"x"}`},
		{name: "invalid label", path: "/logs", body: `{"labels":{"":"value"},"message":"x"}`},
		{name: "unknown field", path: "/logs", body: `{"message":"x","extra":true}`},
		{name: "empty batch", path: "/logs/batch", body: `{"entries":[]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(router, http.MethodPost, test.path, test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
