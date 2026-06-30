package search

import "testing"

const esResp = `{
  "hits": {
    "total": {"value": 3},
    "hits": [
      {"_id":"m1","_source":{"message_id":"m1","group_id":1,"sequence":42,"sender_id":7,"sender_name":"Alice","content":"hello world","message_type":"text","created_at":1719000000000},
       "sort":[1719000000000,42],
       "highlight":{"content":["<em>hello</em> world"]}},
      {"_id":"m2","_source":{"message_id":"m2","group_id":1,"sequence":40,"sender_id":8,"sender_name":"Bob","content":"hello there","message_type":"text","created_at":1718999999000},
       "sort":[1718999999000,40]}
    ]
  }
}`

func TestParseResponseTrimsToPageSizeAndSetsCursor(t *testing.T) {
	// pageSize 1 with 2 hits returned (size+1) => hasMore true, 1 item, cursor from item 1
	res, err := ParseResponse([]byte(esResp), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.HasMore {
		t.Fatal("expected hasMore true when more than pageSize hits returned")
	}
	if len(res.Items) != 1 {
		t.Fatalf("expected 1 item after trim, got %d", len(res.Items))
	}
	if res.Items[0].MessageID != "m1" || res.Items[0].GroupID != 1 || res.Items[0].Sequence != 42 {
		t.Fatalf("unexpected item: %#v", res.Items[0])
	}
	if res.NextCursor == "" {
		t.Fatal("expected next cursor when hasMore")
	}
	dec, _ := DecodeCursor(res.NextCursor)
	if len(dec) != 2 || dec[1] != float64(42) {
		t.Fatalf("cursor should encode last included hit sort, got %#v", dec)
	}
}

func TestParseResponseHighlightFallsBackToContent(t *testing.T) {
	res, err := ParseResponse([]byte(esResp), 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.HasMore {
		t.Fatal("expected hasMore false when hits <= pageSize")
	}
	if len(res.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(res.Items))
	}
	if res.Items[0].Highlight != "<em>hello</em> world" {
		t.Fatalf("expected highlight from ES, got %q", res.Items[0].Highlight)
	}
	// second hit has no highlight => fall back to raw content
	if res.Items[1].Highlight != "hello there" {
		t.Fatalf("expected highlight fallback to content, got %q", res.Items[1].Highlight)
	}
}
