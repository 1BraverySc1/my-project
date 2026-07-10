package tests

import (
	"testing"
	"time"

	"github.com/raftimpl/mini/internal/raft"
)

func TestSingleNodeSubmitAppliesOperation(t *testing.T) {
	applyCh := make(chan raft.ApplyMsg, 1)
	rf, err := raft.NewRaft(1, "127.0.0.1:0", "", applyCh)
	if err != nil {
		t.Fatalf("NewRaft() error = %v", err)
	}
	defer rf.Stop()

	applied := make(chan raft.Op, 1)
	go func() {
		msg := <-applyCh
		applied <- msg.Op
		close(msg.Done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !rf.IsLeader() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !rf.IsLeader() {
		t.Fatal("single-node raft did not become leader")
	}

	op := raft.Op{Type: "put", Key: "k", Value: "v"}
	if _, err := rf.Submit(op); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	select {
	case got := <-applied:
		if got.Type != op.Type || got.Key != op.Key || got.Value != op.Value {
			t.Fatalf("applied operation = %+v, want %+v", got, op)
		}
	case <-time.After(time.Second):
		t.Fatal("operation was not applied")
	}
}
