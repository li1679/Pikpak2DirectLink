package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRequiresJSONMediaTypeAndSingleValue(t *testing.T) {
	var payload struct {
		Name string `json:"name"`
	}

	plain := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"value"}`))
	plain.Header.Set("Content-Type", "text/plain")
	if err := decodeJSON(plain, &payload); err == nil {
		t.Fatal("text/plain JSON body was accepted")
	}

	trailing := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"value"}=`))
	trailing.Header.Set("Content-Type", "application/json")
	if err := decodeJSON(trailing, &payload); err == nil {
		t.Fatal("trailing non-JSON content was accepted")
	}

	valid := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"value"}`))
	valid.Header.Set("Content-Type", "application/problem+json; charset=utf-8")
	if err := decodeJSON(valid, &payload); err != nil {
		t.Fatalf("valid structured JSON media type rejected: %v", err)
	}
	if payload.Name != "value" {
		t.Fatalf("decoded name = %q", payload.Name)
	}
}
