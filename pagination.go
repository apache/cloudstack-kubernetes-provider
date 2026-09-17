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

import "fmt"

// maxListPages bounds a single walk. Because the total is re-read on every page,
// a result set that grows as fast as it is consumed would otherwise keep the
// walk going indefinitely. It is a backstop, not a limit expected to be reached:
// at CloudStack's default page size it allows half a million records.
const maxListPages = 1000

// pageableParams is the paging surface that every cloudstack-go List*Params
// type exposes.
type pageableParams interface {
	SetPage(int)
	SetPagesize(int)
}

// listAll makes further requests to fetch the remaining items if the count is higher
// than the number of items returned. It fails rather than returning a partial
// list, so callers never reconcile against one.
//
// CloudStack offers no cursor, only page and pagesize, so a record removed from
// an earlier page shifts the rest down and one on a page boundary can be missed.
// Repeats, the other half of that, are dropped by dedupeByID at the call sites.
func listAll[T any](p pageableParams, list func() (count int, items []T, err error)) ([]T, error) {
	count, items, err := list()
	if err != nil {
		return nil, err
	}

	// Nothing was truncated, or there is nothing to page through.
	if len(items) >= count || len(items) == 0 {
		return items, nil
	}

	// The server just demonstrated how many records it will return at a time,
	// which is the one page size it is guaranteed to accept.
	pageSize := len(items)
	collected := items

	for page := 2; len(collected) < count; page++ {
		if page > maxListPages {
			return nil, fmt.Errorf("gave up paging after %d pages holding %d of %d records", maxListPages, len(collected), count)
		}

		p.SetPage(page)
		p.SetPagesize(pageSize)

		pageCount, items, err := list()
		if err != nil {
			return nil, err
		}

		// Records may be added while we are paging, which pushes the total up.
		// Track the highest the server has reported so growth cannot cut the
		// walk short; taking the highest rather than the latest also keeps a
		// shrinking total from ending the walk before the pages say so.
		if pageCount > count {
			count = pageCount
		}

		// Records may equally have been removed since the first request, so
		// trust the pages rather than the count and stop as soon as one runs
		// short.
		if len(items) == 0 {
			break
		}

		collected = append(collected, items...)

		if len(items) < pageSize {
			break
		}
	}

	return collected, nil
}

// dedupeByID drops repeats from a walked list, keeping the first of each ID.
//
// A list that changes while it is being walked can return the same record on
// more than one page, and every page decodes into its own structs, so a repeat
// arrives as a second pointer to an equal record. Callers that key bookkeeping
// on the pointer would otherwise treat the two as unrelated records.
func dedupeByID[T any](items []T, id func(T) string) []T {
	unique := make([]T, 0, len(items))
	seen := make(map[string]bool, len(items))

	for _, item := range items {
		key := id(item)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, item)
	}

	return unique
}
