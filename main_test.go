package main

import (
	"reflect"
	"testing"
)

func TestNormalizeDNSDeletions(t *testing.T) {
	got, err := normalizeDNSDeletions([]string{" APP.Example.com. ", "app.example.com", "api.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app.example.com", "api.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeDNSDeletions() = %#v, want %#v", got, want)
	}

	if _, err := normalizeDNSDeletions([]string{"not a hostname"}); err == nil {
		t.Fatal("normalizeDNSDeletions() accepted an invalid hostname")
	}
}

func TestHostnamesFromZoraxyRules(t *testing.T) {
	rules := []zoraxyProxyRule{
		{
			RootOrMatchingDomain: "App.Example.com",
			MatchingDomainAlias:  []string{"www.example.com", "invalid local", "APP.EXAMPLE.COM"},
		},
		{RootOrMatchingDomain: "disabled.example.com", Disabled: true},
		{RootOrMatchingDomain: "api.example.com."},
	}

	got := hostnamesFromZoraxyRules(rules)
	want := []string{"api.example.com", "app.example.com", "www.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hostnamesFromZoraxyRules() = %#v, want %#v", got, want)
	}
}
