// Package modfixture exists solely to give `go mod download` a real external
// dependency to fetch, so acceptance tests can assert GOMODCACHE actually
// gets populated.
package modfixture

import _ "github.com/google/go-cmp/cmp"
