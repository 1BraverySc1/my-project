package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"webdownld_go/internal/meta"
)

func TestFileIDIsScopedByOwner(t *testing.T) {
	alice := meta.FileIDByOwnerAndName("alice", "report.pdf", 42)
	bob := meta.FileIDByOwnerAndName("bob", "report.pdf", 42)
	if alice == bob {
		t.Fatal("file IDs for different owners must differ")
	}
	if alice != meta.FileIDByOwnerAndName("alice", "report.pdf", 42) {
		t.Fatal("file ID must be stable for the same owner, name, and size")
	}
}

func TestRaftClientMapsLeaderIDToHTTPNode(t *testing.T) {
	var leader *httptest.Server
	leader = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("value"))
	}))
	defer leader.Close()

	follower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"leader_id":2,"leader_raft_addr":"127.0.0.1:9001"}`))
	}))
	defer follower.Close()

	nodes := []string{host(follower.URL), host(leader.URL)}
	value, err := meta.NewRaftClient(nodes).Get(context.Background(), "key")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if value != "value" {
		t.Fatalf("Get() = %q, want value", value)
	}
}

func host(rawURL string) string {
	return strings.TrimPrefix(rawURL, "http://")
}
