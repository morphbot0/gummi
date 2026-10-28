package pr

import (
	"context"
	"testing"

	"github.com/morphis/gummi/internal/domain"
)

// TestFetchFeedbackCountsWhatItDidNotRead: comments and reviews past the
// first 100 are counted as unread, and a thread with more comments than
// were read is marked partial, so the trust filter keeps it out.
func TestFetchFeedbackCountsWhatItDidNotRead(t *testing.T) {
	bin := fakeGraphQL(t, `{"data":{"repository":{"pullRequest":{
 "reviewThreads":{"nodes":[
   {"id":"T1","isResolved":false,"comments":{"totalCount":101,"nodes":[{"id":"C1","url":"u1","body":"b","author":{"login":"alice"}}]}},
   {"id":"T2","isResolved":false,"comments":{"totalCount":1,"nodes":[{"id":"C2","url":"u2","body":"b","author":{"login":"alice"}}]}}
 ],"pageInfo":{"hasNextPage":false,"endCursor":null}},
 "comments":{"totalCount":103,"nodes":[{"id":"K1","url":"u3","body":"hi","author":{"login":"alice"}}]},
 "reviews":{"totalCount":2,"nodes":[{"id":"R1","url":"u4","body":"","state":"APPROVED","author":{"login":"alice"}}]}
}}}}`)
	items, unread, err := FetchFeedback(context.Background(), bin, domain.PullRequestRef{Repo: "o/r", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	if unread != 103 {
		t.Errorf("unread = %d, want 102 comments + 1 review", unread)
	}
	partial := map[string]bool{}
	for _, it := range items {
		partial[it.ID] = it.Partial
	}
	if !partial["T1"] || partial["T2"] {
		t.Errorf("partial = %v", partial)
	}
}
