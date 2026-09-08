package jira

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rest/api/3/status" {
			t.Errorf("path = %s, want /rest/api/3/status", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "statuses.json"))
	}))
	defer srv.Close()

	cat, err := newTestClient(t, srv, nil).StatusCatalog(context.Background())
	if err != nil {
		t.Fatalf("StatusCatalog: %v", err)
	}

	if got := cat.NameByID("10007"); got != "Returned" {
		t.Errorf("NameByID(10007) = %q, want Returned", got)
	}
	if !cat.Known("3") || cat.Known("99999") {
		t.Error("Known: expected 3 to exist and 99999 not to")
	}
	// The site has two statuses whose names differ only in case; resolving a
	// configured name must yield both ids rather than silently picking one.
	ids := cat.IDsByName(" ReTuRnEd ")
	if len(ids) != 2 {
		t.Fatalf("IDsByName = %v, want both 10007 and 10008", ids)
	}
	if len(cat.IDsByName("Nope")) != 0 {
		t.Error("IDsByName returned ids for an unknown name")
	}
	if got := cat.Names()["10001"]; got != "Code Review" {
		t.Errorf("Names()[10001] = %q, want Code Review", got)
	}
}
