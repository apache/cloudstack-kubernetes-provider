/*
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package cloudstack

import (
	"errors"
	"reflect"
	"testing"
)

// pagedServer doubles as the params object and the list API, faking a CloudStack
// server that truncates a request carrying no page at default.page.size.
type pagedServer struct {
	items []int
	// pageSize is the server's default.page.size.
	pageSize int
	// count overrides the reported total when non-zero, to model a server whose
	// count disagrees with the records it actually hands out.
	count int

	// afterEach runs once a response has been built, to model a result set that
	// changes while it is being walked.
	afterEach func(s *pagedServer)

	// Paging parameters as set by listAll. Zero means "not sent".
	page    int
	reqSize int

	// requests records the (page, pagesize) pair of every call.
	requests [][2]int
}

func (s *pagedServer) SetPage(v int)     { s.page = v }
func (s *pagedServer) SetPagesize(v int) { s.reqSize = v }

func (s *pagedServer) list() (int, []int, error) {
	s.requests = append(s.requests, [2]int{s.page, s.reqSize})

	total := s.count
	if total == 0 {
		total = len(s.items)
	}

	// No page requested: the server applies default.page.size from page one.
	page, size := s.page, s.reqSize
	if page == 0 {
		page, size = 1, s.pageSize
	}

	defer func() {
		if s.afterEach != nil {
			s.afterEach(s)
		}
	}()

	start := (page - 1) * size
	if start >= len(s.items) {
		return total, nil, nil
	}

	end := start + size
	if end > len(s.items) {
		end = len(s.items)
	}

	return total, s.items[start:end], nil
}

// grow appends n further records, as if they had been created elsewhere while
// the walk was in progress.
func (s *pagedServer) grow(n int) {
	for i := 0; i < n; i++ {
		s.items = append(s.items, len(s.items))
	}
}

func seq(n int) []int {
	items := make([]int, n)
	for i := range items {
		items[i] = i
	}
	return items
}

func TestListAll(t *testing.T) {
	tests := []struct {
		name         string
		server       pagedServer
		want         []int
		wantRequests [][2]int
	}{
		{
			name:         "fits in one page",
			server:       pagedServer{items: seq(3), pageSize: 500},
			want:         seq(3),
			wantRequests: [][2]int{{0, 0}},
		},
		{
			name:         "empty result",
			server:       pagedServer{items: nil, pageSize: 500},
			want:         nil,
			wantRequests: [][2]int{{0, 0}},
		},
		{
			name:         "exactly one full page",
			server:       pagedServer{items: seq(500), pageSize: 500},
			want:         seq(500),
			wantRequests: [][2]int{{0, 0}},
		},
		{
			name:         "truncated, partial second page",
			server:       pagedServer{items: seq(750), pageSize: 500},
			want:         seq(750),
			wantRequests: [][2]int{{0, 0}, {2, 500}},
		},
		{
			name:         "truncated, exact page multiple",
			server:       pagedServer{items: seq(1000), pageSize: 500},
			want:         seq(1000),
			wantRequests: [][2]int{{0, 0}, {2, 500}},
		},
		{
			name:         "several pages",
			server:       pagedServer{items: seq(12), pageSize: 5},
			want:         seq(12),
			wantRequests: [][2]int{{0, 0}, {2, 5}, {3, 5}},
		},
		{
			name:         "a page size of one still terminates",
			server:       pagedServer{items: seq(3), pageSize: 1},
			want:         seq(3),
			wantRequests: [][2]int{{0, 0}, {2, 1}, {3, 1}},
		},
		{
			// Records removed between requests: the walk stops on the short page
			// rather than spinning until the stale count is reached.
			name:         "count overstates what the server returns",
			server:       pagedServer{items: seq(7), pageSize: 5, count: 100},
			want:         seq(7),
			wantRequests: [][2]int{{0, 0}, {2, 5}},
		},
		{
			// Every page is full but the count is never reached, so termination
			// has to come from the first empty page.
			name:         "count overstates on an exact page boundary",
			server:       pagedServer{items: seq(10), pageSize: 5, count: 100},
			want:         seq(10),
			wantRequests: [][2]int{{0, 0}, {2, 5}, {3, 5}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.server

			got, err := listAll(&server, server.list)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("items = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(server.requests, tt.wantRequests) {
				t.Errorf("requests = %v, want %v", server.requests, tt.wantRequests)
			}
		})
	}
}

func TestListAllFollowsAGrowingResultSet(t *testing.T) {
	// The count reported on the first page goes stale as soon as records are
	// added, so a walk that trusts only that first total stops early. Here five
	// records appear after the first request: a walk pinned to the original
	// total of 10 would return 10 of the 15 that exist.
	server := &pagedServer{items: seq(10), pageSize: 5}
	server.afterEach = func(s *pagedServer) {
		if len(s.requests) == 1 {
			s.grow(5)
		}
	}

	got, err := listAll(server, server.list)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 15 {
		t.Errorf("collected %d records, want 15 - the walk stopped at a stale total", len(got))
	}
	if !reflect.DeepEqual(got, seq(15)) {
		t.Errorf("items = %v, want %v", got, seq(15))
	}
}

func TestListAllStopsGrowingResultSetRunningAway(t *testing.T) {
	// Re-reading the total on every page means a set that grows exactly as fast
	// as it is consumed would never satisfy the loop condition. The page cap is
	// what guarantees the walk still terminates.
	server := &pagedServer{items: seq(10), pageSize: 5}
	server.afterEach = func(s *pagedServer) { s.grow(5) }

	got, err := listAll(server, server.list)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(server.requests) != maxListPages {
		t.Errorf("made %d requests, want the walk capped at %d", len(server.requests), maxListPages)
	}
	// Whatever it managed to collect must still be the real prefix, not garbage.
	if !reflect.DeepEqual(got, seq(len(got))) {
		t.Errorf("collected records are not a contiguous prefix: %v", got)
	}
}

func TestListAllNeverSendsPageZero(t *testing.T) {
	// CloudStack rejects a page parameter that is merely present, so page=0 is
	// an error rather than a way of asking for the first page. The walk starts
	// at 2, which makes that unrepresentable.
	server := &pagedServer{items: seq(150), pageSize: 50}

	if _, err := listAll(server, server.list); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(server.requests) < 2 {
		t.Fatalf("expected the result to be paged, got %v", server.requests)
	}
	if first := server.requests[0]; first != [2]int{0, 0} {
		t.Errorf("first request = %v, want no paging parameters at all", first)
	}
	for _, request := range server.requests[1:] {
		if request[0] < 2 {
			t.Errorf("request %v used page %d, want >= 2", request, request[0])
		}
	}
}

func TestListAllPagesWithTheServersOwnPageSize(t *testing.T) {
	// pagesize may not exceed default.page.size, so the walk has to reuse the
	// length the server itself returned rather than a fixed value.
	server := &pagedServer{items: seq(150), pageSize: 50}

	if _, err := listAll(server, server.list); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, request := range server.requests[1:] {
		if request[1] != 50 {
			t.Errorf("request %v used pagesize %d, want 50", request, request[1])
		}
	}
}

func TestListAllError(t *testing.T) {
	wantErr := errors.New("boom")
	server := &pagedServer{}

	t.Run("on the first request", func(t *testing.T) {
		got, err := listAll(server, func() (int, []int, error) {
			return 0, nil, wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Errorf("err = %v, want %v", err, wantErr)
		}
		if got != nil {
			t.Errorf("items = %v, want nil", got)
		}
	})

	t.Run("on a later page", func(t *testing.T) {
		calls := 0
		got, err := listAll(server, func() (int, []int, error) {
			calls++
			if calls == 1 {
				return 750, seq(500), nil
			}
			return 0, nil, wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Errorf("err = %v, want %v", err, wantErr)
		}
		// A partial result is worse than no result: callers of these lists treat
		// a missing record as "does not exist" and create or delete accordingly.
		if got != nil {
			t.Errorf("items = %v, want nil", got)
		}
	})
}
