package gitops

import "testing"

// Broadcast fans out through buffered channels with non-blocking sends, so
// these tests read synchronously with no goroutines involved (and therefore
// nothing for TestMain's goleak check to catch).

func TestLogHubBroadcastReachesPerRepoAndFirehoseSubscribers(t *testing.T) {
	h := &LogHub{subscribers: make(map[string][]chan string)}

	repoChan := h.Subscribe("repo-1")
	defer h.Unsubscribe("repo-1", repoChan)
	fireChan := h.SubscribeAll()
	defer h.UnsubscribeAll(fireChan)

	h.Broadcast("repo-1", "Repo One", "hello world")

	select {
	case msg := <-repoChan:
		if msg != "hello world" {
			t.Errorf("per-repo subscriber got %q, want %q", msg, "hello world")
		}
	default:
		t.Fatal("per-repo subscriber received nothing")
	}

	select {
	case entry := <-fireChan:
		if entry.RepositoryID != "repo-1" || entry.RepositoryName != "Repo One" || entry.Line != "hello world" {
			t.Errorf("firehose subscriber got %+v, want {repo-1 Repo One hello world ...}", entry)
		}
	default:
		t.Fatal("firehose subscriber received nothing")
	}
}

func TestLogHubBroadcastDoesNotCrossRepositories(t *testing.T) {
	h := &LogHub{subscribers: make(map[string][]chan string)}

	chanA := h.Subscribe("repo-a")
	defer h.Unsubscribe("repo-a", chanA)
	chanB := h.Subscribe("repo-b")
	defer h.Unsubscribe("repo-b", chanB)

	h.Broadcast("repo-a", "Repo A", "only for A")

	select {
	case msg := <-chanA:
		if msg != "only for A" {
			t.Errorf("repo-a subscriber got %q", msg)
		}
	default:
		t.Fatal("repo-a subscriber received nothing")
	}

	select {
	case msg := <-chanB:
		t.Errorf("repo-b subscriber unexpectedly received %q from a repo-a broadcast", msg)
	default:
		// Expected: nothing crossed over.
	}
}

func TestLogHubUnsubscribeDoesNotAffectOtherSubscribers(t *testing.T) {
	h := &LogHub{subscribers: make(map[string][]chan string)}

	chan1 := h.Subscribe("repo-1")
	chan2 := h.Subscribe("repo-1")
	defer h.Unsubscribe("repo-1", chan2)

	h.Unsubscribe("repo-1", chan1)
	if _, open := <-chan1; open {
		t.Errorf("chan1 should be closed after Unsubscribe")
	}

	h.Broadcast("repo-1", "Repo One", "still here")
	select {
	case msg := <-chan2:
		if msg != "still here" {
			t.Errorf("chan2 got %q, want %q", msg, "still here")
		}
	default:
		t.Fatal("chan2 should still receive broadcasts after a sibling subscriber unsubscribed")
	}
}

func TestLogHubUnsubscribeAllDoesNotAffectPerRepoSubscribers(t *testing.T) {
	h := &LogHub{subscribers: make(map[string][]chan string)}

	repoChan := h.Subscribe("repo-1")
	defer h.Unsubscribe("repo-1", repoChan)
	fireChan := h.SubscribeAll()

	h.UnsubscribeAll(fireChan)
	if _, open := <-fireChan; open {
		t.Errorf("fireChan should be closed after UnsubscribeAll")
	}

	h.Broadcast("repo-1", "Repo One", "post-unsubscribe-all")
	select {
	case msg := <-repoChan:
		if msg != "post-unsubscribe-all" {
			t.Errorf("repoChan got %q", msg)
		}
	default:
		t.Fatal("per-repo subscriber should still receive broadcasts after the firehose unsubscribed")
	}
}
