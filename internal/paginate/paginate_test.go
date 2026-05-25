package paginate

import (
	"context"
	"testing"

	smplkit "github.com/smplkit/go-sdk/v3"
)

// stub records each invocation and serves canned pages so we can
// assert pagination walks correctly.
type stub struct {
	pages [][]int
	calls []callRecord
}

type callRecord struct {
	page int
}

func (s *stub) fetch(_ context.Context, opts ...smplkit.ListOption) ([]int, error) {
	// We can't read private SDK option fields, so the stub uses call
	// order as page number and infers the size from the page it serves.
	s.calls = append(s.calls, callRecord{page: len(s.calls) + 1})
	if len(s.calls) > len(s.pages) {
		return nil, nil
	}
	return s.pages[len(s.calls)-1], nil
}

func TestAll_StopsOnShortPage(t *testing.T) {
	// pageSize defaults to 1000; serving a short page on the first
	// call should terminate immediately.
	s := &stub{pages: [][]int{{1, 2, 3}}}
	got, err := All(context.Background(), s.fetch, 0)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d items, want 3", len(got))
	}
	if len(s.calls) != 1 {
		t.Errorf("expected 1 call (short page), got %d", len(s.calls))
	}
}

func TestSingle_RespectsLimit(t *testing.T) {
	s := &stub{pages: [][]int{{1, 2}}}
	got, err := Single(context.Background(), s.fetch, 50)
	if err != nil {
		t.Fatalf("Single: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
	if len(s.calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(s.calls))
	}
}

func TestAll_RespectsLimit_ShortPageWins(t *testing.T) {
	// limit=1 caps page size to 1; one short page (zero or fewer items)
	// terminates the loop on the first iteration.
	s := &stub{pages: [][]int{{}}}
	got, err := All(context.Background(), s.fetch, 1)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}
