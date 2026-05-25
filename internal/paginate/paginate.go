// Package paginate walks SDK list endpoints page-by-page.
//
// The SDK's List(ctx, opts...) returns one page per call. The CLI
// exposes `--all` to consume every page, and `--limit` to cap the page
// size. paginate.All wraps that loop so each noun's list command
// stays small.
package paginate

import (
	"context"

	smplkit "github.com/smplkit/go-sdk/v3"
)

// DefaultPageSize is the page size the CLI requests when --limit is
// not set. Matches the SDK's fetchAllPageSize so a single round trip
// is enough for the vast majority of accounts.
const DefaultPageSize = 1000

// Fetcher returns one page of T. Implementations are tiny shims around
// `mgmt.<Ns>().List(ctx, opts...)` per noun.
type Fetcher[T any] func(ctx context.Context, opts ...smplkit.ListOption) ([]T, error)

// All walks pages until a short page or empty page is seen, returning
// every item. limit, when > 0, caps the per-page request size.
func All[T any](ctx context.Context, fetch Fetcher[T], limit int) ([]T, error) {
	pageSize := DefaultPageSize
	if limit > 0 && limit < pageSize {
		pageSize = limit
	}
	var out []T
	for page := 1; ; page++ {
		items, err := fetch(ctx, smplkit.WithPageNumber(page), smplkit.WithPageSize(pageSize))
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		// Stop when the server gave us less than a full page (the
		// classic offset-pagination terminator) or when an empty page
		// arrives (defensive guard for limit==1 / off-by-one cases).
		if len(items) < pageSize {
			break
		}
	}
	return out, nil
}

// Single returns one page using the SDK's defaults plus an optional
// --limit page size.
func Single[T any](ctx context.Context, fetch Fetcher[T], limit int) ([]T, error) {
	opts := []smplkit.ListOption{}
	if limit > 0 {
		opts = append(opts, smplkit.WithPageSize(limit))
	}
	return fetch(ctx, opts...)
}
