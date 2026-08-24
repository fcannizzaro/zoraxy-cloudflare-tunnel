package cloudflare

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestDeleteCNAME(t *testing.T) {
	requests := []string{}
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		var body string
		switch r.Method {
		case http.MethodGet:
			if got := r.URL.Query().Get("name"); got != "app.example.com" {
				return nil, fmt.Errorf("DNS lookup name = %q", got)
			}
			body = `{"success":true,"result":[{"id":"record-id","type":"CNAME","name":"app.example.com"}]}`
		case http.MethodDelete:
			body = `{"success":true,"result":{"id":"record-id"}}`
		default:
			return nil, fmt.Errorf("unexpected method %s", r.Method)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})

	client := New("token", "account", "zone")
	client.APIBaseURL = "https://cloudflare.test"
	client.HTTP = &http.Client{Transport: transport}
	removed, err := client.DeleteCNAME(context.Background(), "app.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !removed {
		t.Fatal("DeleteCNAME() reported that the existing record was not removed")
	}
	want := []string{
		"GET /zones/zone/dns_records",
		"DELETE /zones/zone/dns_records/record-id",
	}
	if len(requests) != len(want) {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("requests = %#v, want %#v", requests, want)
		}
	}
}
