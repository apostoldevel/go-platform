package pgtx

import (
	"context"
	"testing"
)

// onDaemonRoad has two witnesses, and each alone must put a call on the
// daemon road: the transaction handed to the handler (daemonTx) and the mark
// in its context (roadKey — a nested tx.Begin of the handler loses the
// wrapper but keeps the context). An integration test cannot tell them apart:
// doDaemon always sets both.
func TestOnDaemonRoad_EachWitnessAlone(t *testing.T) {
	bare := context.Background()
	marked := context.WithValue(bare, roadKey{}, true)
	if onDaemonRoad(bare, nil) {
		t.Fatal("neither witness: the direct road")
	}
	if !onDaemonRoad(bare, daemonTx{}) {
		t.Fatal("daemonTx alone must be the daemon road")
	}
	if !onDaemonRoad(marked, nil) {
		t.Fatal("the context mark alone must be the daemon road (a nested transaction of the handler)")
	}
	if !onDaemonRoad(marked, daemonTx{}) {
		t.Fatal("both witnesses: the daemon road")
	}
}
