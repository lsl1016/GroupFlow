package repo

import "testing"

func TestNextCursorUsesLastIncludedRow(t *testing.T) {
	ids, next := paginateActiveMemberIDs([]memberIDRow{{rowID: 1, userID: 101}, {rowID: 2, userID: 102}, {rowID: 3, userID: 103}}, 2)
	if len(ids) != 2 || ids[0] != 101 || ids[1] != 102 {
		t.Fatalf("unexpected ids: %#v", ids)
	}
	if next != 2 {
		t.Fatalf("expected next cursor 2, got %d", next)
	}
}

func TestNextCursorZeroWhenPageNotFull(t *testing.T) {
	ids, next := paginateActiveMemberIDs([]memberIDRow{{rowID: 1, userID: 101}}, 2)
	if len(ids) != 1 || ids[0] != 101 {
		t.Fatalf("unexpected ids: %#v", ids)
	}
	if next != 0 {
		t.Fatalf("expected next cursor 0, got %d", next)
	}
}
