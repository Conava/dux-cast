package main

import "testing"

func TestLookAhead(t *testing.T) {
	m := newMediaSession(nil, device{}, []queueSource{{Source: "/a.jpg"}, {Source: "/b.jpg"}, {Source: "/c.mp4"}, {Source: "/d.jpg"}, {Source: "/e.mp4"}}, 0, queueOptions{})
	order := func() (got []int) {
		for i := m.nextToPrepare(); i >= 0; i = m.nextToPrepare() {
			got = append(got, i)
			m.items[i].status = "ready"
		}
		return got
	}
	// The current photo, the next photo, and a video two ahead is skipped.
	if got := order(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("from 0: %v", got)
	}
	m.index = 3
	m.items[0].files = []string{"/nonexistent"}
	if got := order(); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Errorf("from 3: %v", got)
	}
	// Items more than two behind give their prepared copies back.
	m.prune()
	if m.items[0].status != "waiting" || m.items[1].status != "ready" && m.items[1].files != nil {
		t.Errorf("prune: %s %s", m.items[0].status, m.items[1].status)
	}
	// Looping wraps the look-ahead around the end.
	m.opts.Repeat, m.index = "show", 4
	m.items[0].status = "waiting"
	if got := order(); len(got) != 1 || got[0] != 0 {
		t.Errorf("loop from 4: %v", got)
	}
}

func TestEdit(t *testing.T) {
	srcs := func(names ...string) (s []queueSource) {
		for _, n := range names {
			s = append(s, queueSource{Source: "/" + n})
		}
		return s
	}
	m := newMediaSession(nil, device{}, srcs("a.jpg", "b.jpg", "c.mp4"), 1, queueOptions{})
	a, b, c := m.items[0], m.items[1], m.items[2]
	b.status, c.status = "ready", "ready"
	c.files = []string{"/nonexistent"}

	// Moving the current item keeps it playing and its prepared state.
	if m.edit(srcs("b.jpg", "a.jpg", "c.mp4"), 0) {
		t.Error("reorder: wants another item shown")
	}
	if m.items[0] != b || m.items[1] != a || m.items[2] != c || m.index != 0 || b.status != "ready" {
		t.Errorf("reorder: %v index %d", m.sources(), m.index)
	}

	// A dropped item gives its prepared copy back; a new one starts waiting.
	if m.edit(srcs("b.jpg", "d.jpg"), 0) {
		t.Error("drop: wants another item shown")
	}
	if c.files != nil || c.status != "waiting" || m.items[1].status != "waiting" || m.indexOf(c) != -1 {
		t.Errorf("drop: c %s %v, d %s", c.status, c.files, m.items[1].status)
	}

	// Dropping the current item shows the one at index.
	if !m.edit(srcs("a.jpg", "d.jpg"), 1) || m.items[m.index].source != "/d.jpg" {
		t.Errorf("drop current: %v index %d", m.sources(), m.index)
	}

	// The current item, listed twice, stays the one at index.
	d := m.items[1]
	if m.edit(srcs("d.jpg", "a.jpg", "d.jpg"), 2) || m.items[2] != d || m.items[0] == d {
		t.Errorf("duplicate: index %d", m.index)
	}
}

func TestRepeat(t *testing.T) {
	srcs := []queueSource{{Source: "/a.mp3"}, {Source: "/b.mp3"}, {Source: "/c.mp3"}}
	m := newMediaSession(nil, device{}, srcs, 2, queueOptions{})
	if m.afterEnd() != -1 || m.stepTo(-1) != 1 {
		t.Error("off: the show ends after the last item")
	}

	// The show once: one more round, then it ends.
	m = newMediaSession(nil, device{}, srcs, 2, queueOptions{Repeat: "show", Times: 1})
	if m.afterEnd() != 0 || m.wraps() {
		t.Errorf("show once: first wrap, then no more")
	}
	if m.afterEnd() != -1 {
		t.Error("show once: second wrap")
	}

	// Until stopped, it keeps going round, also backwards.
	m = newMediaSession(nil, device{}, srcs, 0, queueOptions{Loop: true})
	if m.stepTo(-1) != 2 {
		t.Error("loop: wraps backwards")
	}
	m.index = 2
	if m.afterEnd() != 0 {
		t.Error("loop: wraps forwards")
	}

	// The item once plays one more time, then the show moves on.
	m = newMediaSession(nil, device{}, srcs, 1, queueOptions{Repeat: "item", Times: 1})
	if m.afterEnd() != 1 || m.afterEnd() != 2 {
		t.Error("item once")
	}
	// Until stopped it stays, unless it failed.
	m = newMediaSession(nil, device{}, srcs, 1, queueOptions{Repeat: "item"})
	if m.afterEnd() != 1 || m.afterEnd() != 1 {
		t.Error("item until stopped")
	}
	m.items[1].status = "failed"
	if m.afterEnd() != 2 {
		t.Error("a failed item isn't repeated")
	}
}
