package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/messenger"
	"github.com/ericflo/finalechat/internal/store"
)

// The relay delivers new agent messages and questions to linked Messenger
// chats. One replica relays at a time (a session advisory lock); the
// database is the source of truth and bus events only make it look sooner,
// the same rule the long-polls follow.

const messengerRelayLock = 7318001

const (
	// messengerBacklog is how many relayable items at once become a digest
	// instead of a burst of messages.
	messengerBacklog = 12
	// messengerPiecesPerSend is how many chat bubbles one message may take
	// before the rest waits for /more.
	messengerPiecesPerSend = 3
)

// kickMessenger asks the relay (if it runs in this replica) to look now.
func (s *Server) kickMessenger(userID uuid.UUID) {
	select {
	case s.messengerKick <- userID:
	default:
	}
}

// RunMessengerRelay relays until ctx ends. It is a no-op without the
// connector configured.
func (s *Server) RunMessengerRelay(ctx context.Context) {
	if s.messenger == nil {
		return
	}
	for ctx.Err() == nil {
		if conn, err := s.store.Pool().Acquire(ctx); err == nil {
			var leader bool
			if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", messengerRelayLock).Scan(&leader); err == nil && leader {
				s.log.Info("messenger relay: leading")
				s.relayLoop(ctx, conn)
				_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", messengerRelayLock)
			}
			conn.Release()
		}
		sleepCtx(ctx, 15*time.Second)
	}
}

func (s *Server) relayLoop(ctx context.Context, conn *pgxpool.Conn) {
	events := make(chan bus.Event, 256)
	subs := map[string]func(){}
	defer func() {
		for _, cancel := range subs {
			cancel()
		}
	}()
	subscribe := func() {
		links, err := s.store.ListMessengerLinks(ctx)
		if err != nil {
			return
		}
		live := map[string]bool{}
		for _, l := range links {
			id := l.UserID.String()
			live[id] = true
			if subs[id] != nil {
				continue
			}
			ch, cancel := s.bus.Subscribe(id)
			subs[id] = cancel
			go func() {
				for ev := range ch {
					select {
					case events <- ev:
					default:
					}
				}
			}()
		}
		for id, cancel := range subs {
			if !live[id] {
				cancel()
				delete(subs, id)
			}
		}
	}
	subscribe()
	s.messengerRelayOnce(ctx)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	health := time.NewTicker(30 * time.Second)
	defer health.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	soon := time.NewTimer(time.Hour)
	soon.Stop()
	armed := false
	arm := func() {
		if !armed {
			armed = true
			soon.Reset(s.messengerSettle + 300*time.Millisecond)
		}
	}
	typed := map[uuid.UUID]time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.shutdown:
			return
		case <-health.C:
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				s.log.Warn("messenger relay: lost its lock connection", "err", err)
				return
			}
		case <-prune.C:
			if err := s.store.PruneMessenger(ctx); err != nil {
				s.log.Warn("messenger prune", "err", err)
			}
		case <-tick.C:
			subscribe()
			s.messengerRelayOnce(ctx)
		case <-soon.C:
			armed = false
			s.messengerRelayOnce(ctx)
		case <-s.messengerKick:
			arm()
		case ev := <-events:
			switch ev.Type {
			case bus.MessageCreated, bus.QuestionCreated:
				arm()
			case bus.ThreadActivity:
				s.messengerTyping(ctx, ev, typed)
			}
		}
	}
}

// messengerTyping shows Messenger's typing dots while the thread replies go
// to is busy, at most every 15 seconds (the dots last about 20).
func (s *Server) messengerTyping(ctx context.Context, ev bus.Event, typed map[uuid.UUID]time.Time) {
	if len(ev.Activity) == 0 || string(ev.Activity) == "null" {
		return
	}
	var a store.Activity
	if json.Unmarshal(ev.Activity, &a) != nil || a.Kind == store.ActivityWaiting {
		return
	}
	userID, err := uuid.Parse(ev.UserID)
	if err != nil || time.Since(typed[userID]) < 15*time.Second {
		return
	}
	link, err := s.store.GetMessengerLink(ctx, userID)
	if err != nil || link.Target() == nil || link.Target().String() != ev.ThreadID || !s.messengerWindowOpen(link) {
		return
	}
	typed[userID] = time.Now()
	_ = s.messenger.SenderAction(ctx, link.PSID, "typing_on")
}

func (s *Server) messengerWindowOpen(l *store.MessengerLink) bool {
	if l.WindowClosedAt != nil && !l.WindowClosedAt.Before(l.LastInboundAt) {
		return false
	}
	// Messenger allows 24 hours after the person's last message; stop a
	// little early rather than collect refusals.
	return time.Since(l.LastInboundAt) < 24*time.Hour-5*time.Minute
}

// messengerRelayOnce relays everything pending for every link.
func (s *Server) messengerRelayOnce(ctx context.Context) {
	links, err := s.store.ListMessengerLinks(ctx)
	if err != nil {
		s.log.Warn("messenger relay", "err", err)
		return
	}
	for _, l := range links {
		if err := s.relayLink(ctx, l); err != nil {
			s.log.Warn("messenger relay", "err", err)
		}
	}
}

type relayItem struct {
	at       time.Time
	id       uuid.UUID
	message  *store.Message
	question *store.Question
}

func (s *Server) relayLink(ctx context.Context, link *store.MessengerLink) error {
	if !s.messengerWindowOpen(link) {
		return nil
	}
	msgs, err := s.store.MessagesAfter(ctx, link.UserID, link.MessageAt, link.MessageID, 60, s.messengerSettle)
	if err != nil {
		return err
	}
	qs, err := s.store.QuestionsAfter(ctx, link.UserID, link.QuestionAt, link.QuestionID, 60, s.messengerSettle)
	if err != nil {
		return err
	}
	if len(msgs) == 0 && len(qs) == 0 {
		return nil
	}
	items := make([]relayItem, 0, len(msgs)+len(qs))
	for _, m := range msgs {
		items = append(items, relayItem{at: m.CreatedAt, id: m.ID, message: m})
	}
	for _, q := range qs {
		items = append(items, relayItem{at: q.CreatedAt, id: q.ID, question: q})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.Before(items[j].at) })

	threads := map[uuid.UUID]*store.Thread{}
	threadOf := func(id uuid.UUID) *store.Thread {
		if t, ok := threads[id]; ok {
			return t
		}
		t, err := s.store.GetThread(ctx, link.UserID, id)
		if err != nil {
			t = nil
		}
		threads[id] = t
		return t
	}
	wanted := 0
	for _, it := range items {
		if s.relayWants(link, threadOf, it) {
			wanted++
		}
	}
	if wanted > messengerBacklog {
		return s.relayDigest(ctx, link, items, threadOf)
	}
	for _, it := range items {
		if s.relayWants(link, threadOf, it) {
			var err error
			if it.message != nil {
				_, err = s.messengerSendPieces(ctx, link, threadOf(it.message.ThreadID), it.message, 0)
			} else {
				err = s.messengerSendQuestion(ctx, link, threadOf(it.question.ThreadID), it.question)
			}
			if stop, err := s.relayFailed(ctx, link, err); stop {
				return err
			}
		}
		if err := s.advance(ctx, link, it); err != nil {
			return err
		}
	}
	return nil
}

// relayFailed decides what a send error means: stop and retry later
// (transport trouble, throttling, a closed window), or skip the item (a
// refusal that retrying cannot fix).
func (s *Server) relayFailed(ctx context.Context, link *store.MessengerLink, err error) (stop bool, _ error) {
	if err == nil {
		return false, nil
	}
	if messenger.IsWindowClosed(err) {
		return true, s.store.CloseMessengerWindow(ctx, link.UserID)
	}
	var ge *messenger.Error
	if errors.As(err, &ge) && ge.Status >= 400 && ge.Status < 500 && !ge.RateLimited() && ge.Status != http.StatusUnauthorized {
		s.log.Warn("messenger: skipping an item Messenger refused", "err", err)
		return false, nil
	}
	return true, err
}

func (s *Server) advance(ctx context.Context, link *store.MessengerLink, it relayItem) error {
	id := it.id
	if it.message != nil {
		link.MessageAt, link.MessageID = it.at, &id
		return s.store.AdvanceMessengerCursors(ctx, link.UserID, &it.at, &id, nil, nil)
	}
	link.QuestionAt, link.QuestionID = it.at, &id
	return s.store.AdvanceMessengerCursors(ctx, link.UserID, nil, nil, &it.at, &id)
}

// relayWants is the relay's filter: agent speech and questions, never the
// user's own words (from here, the app, or a terminal an agent mirrors).
func (s *Server) relayWants(link *store.MessengerLink, threadOf func(uuid.UUID) *store.Thread, it relayItem) bool {
	if q := it.question; q != nil {
		t := threadOf(q.ThreadID)
		return t != nil && !t.Muted && q.Status == store.QuestionPending
	}
	m := it.message
	t := threadOf(m.ThreadID)
	if t == nil || t.Muted || m.Deleted || m.Sender == store.SenderUser {
		return false
	}
	if m.Sender == store.SenderSystem {
		kind, _ := m.Meta["kind"].(string)
		return kind == "session_end" && !link.ImportantOnly
	}
	return !link.ImportantOnly || m.Importance == store.ImportanceImportant
}

// messengerPieces renders a message as chat bubbles.
func (s *Server) messengerPieces(ctx context.Context, userID uuid.UUID, thread *store.Thread, m *store.Message) []string {
	tag := s.messengerTag(ctx, userID, thread)
	if kind, _ := m.Meta["kind"].(string); m.Sender == store.SenderSystem && kind == "session_end" {
		return []string{"⏹ " + tag + " " + messenger.Clip(strings.TrimSpace(m.Body), 300)}
	}
	body := strings.TrimSpace(m.Body)
	if m.Format != "text" {
		body = messenger.Markdown(body)
	}
	var b strings.Builder
	if m.Importance == store.ImportanceImportant {
		b.WriteString("❗ ")
	}
	b.WriteString(tag)
	if body != "" {
		b.WriteString("\n" + body)
	}
	for _, a := range m.Attachments {
		fmt.Fprintf(&b, "\n📎 %s (%s)", a.Filename, shortSize(a.Size))
	}
	// Leave room for the "/more" note on the last bubble of a send.
	return messenger.Chunk(b.String(), messenger.MaxTextRunes-40)
}

// shortSize renders a file size for a chat line.
func shortSize(n int) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.Itoa(n>>10) + " KB"
	}
	return strconv.Itoa(n) + " B"
}

// messengerSendPieces sends up to messengerPiecesPerSend bubbles of a
// message starting at from, leaving the rest for /more.
func (s *Server) messengerSendPieces(ctx context.Context, link *store.MessengerLink, thread *store.Thread, m *store.Message, from int) (int, error) {
	pieces := s.messengerPieces(ctx, link.UserID, thread, m)
	if from >= len(pieces) {
		return 0, nil
	}
	end := min(from+messengerPiecesPerSend, len(pieces))
	sent := 0
	for i := from; i < end; i++ {
		text := pieces[i]
		if i == end-1 && end < len(pieces) {
			text += fmt.Sprintf("\n\n… /more (%d more)", len(pieces)-end)
		}
		mid, err := s.messenger.SendText(ctx, link.PSID, text, nil, "")
		if err != nil {
			return sent, err
		}
		sent++
		_ = s.store.RecordMessengerSent(ctx, link.UserID, mid, &thread.ID, nil)
	}
	state := store.JSON{}
	for k, v := range link.State {
		state[k] = v
	}
	if end < len(pieces) {
		state["more"] = map[string]any{"message_id": m.ID.String(), "next": end}
	} else {
		delete(state, "more")
	}
	link.State = state
	if err := s.store.SetMessengerState(ctx, link.UserID, state); err != nil {
		return sent, err
	}
	link.LastThreadID = &thread.ID
	return sent, s.store.SetMessengerLastThread(ctx, link.UserID, thread.ID)
}

// messengerSendQuestion sends a question with numbered options, one quick
// reply per option and Skip.
func (s *Server) messengerSendQuestion(ctx context.Context, link *store.MessengerLink, thread *store.Thread, q *store.Question) error {
	var b strings.Builder
	b.WriteString("❓ " + s.messengerTag(ctx, link.UserID, thread) + "\n" + strings.TrimSpace(messenger.Markdown(q.Prompt)))
	var quick []messenger.QuickReply
	if len(q.Options) > 0 {
		b.WriteString("\n")
	}
	for i, o := range q.Options {
		fmt.Fprintf(&b, "\n%d. %s", i+1, o.Label)
		if o.Description != "" {
			b.WriteString(" — " + o.Description)
		}
		if len(quick) < messenger.MaxQuickReplies-1 {
			quick = append(quick, messenger.QuickReply{Title: strconv.Itoa(i+1) + " " + o.Label, Payload: "a:" + q.ID.String() + ":" + strconv.Itoa(i)})
		}
	}
	var hints []string
	if q.MultiSelect {
		hints = append(hints, "Several apply? Reply with numbers like 1,3.")
	}
	if q.AllowFreeform {
		hints = append(hints, "Or just type an answer.")
	}
	hints = append(hints, "Skip to let the agent decide.")
	b.WriteString("\n\n" + strings.Join(hints, " "))
	quick = append(quick, messenger.QuickReply{Title: "Skip", Payload: "d:" + q.ID.String()})

	pieces := messenger.Chunk(b.String(), messenger.MaxTextRunes)
	for i, text := range pieces {
		var qr []messenger.QuickReply
		if i == len(pieces)-1 {
			qr = quick
		}
		mid, err := s.messenger.SendText(ctx, link.PSID, text, qr, "")
		if err != nil {
			return err
		}
		_ = s.store.RecordMessengerSent(ctx, link.UserID, mid, &thread.ID, &q.ID)
	}
	link.LastThreadID = &thread.ID
	return s.store.SetMessengerLastThread(ctx, link.UserID, thread.ID)
}

// relayDigest summarizes a backlog (after a lapsed window, say) as one
// message per thread count, then re-sends only the questions still open.
func (s *Server) relayDigest(ctx context.Context, link *store.MessengerLink, items []relayItem, threadOf func(uuid.UUID) *store.Thread) error {
	type summary struct {
		thread *store.Thread
		count  int
		last   string
	}
	var order []uuid.UUID
	byThread := map[uuid.UUID]*summary{}
	var open []relayItem
	for _, it := range items {
		if !s.relayWants(link, threadOf, it) {
			continue
		}
		if it.question != nil {
			open = append(open, it)
			continue
		}
		t := threadOf(it.message.ThreadID)
		sm := byThread[t.ID]
		if sm == nil {
			sm = &summary{thread: t}
			byThread[t.ID] = sm
			order = append(order, t.ID)
		}
		sm.count++
		sm.last = store.Preview(it.message.Body)
	}
	if len(order) > 0 {
		var b strings.Builder
		b.WriteString("While you were away:")
		for _, id := range order {
			sm := byThread[id]
			fmt.Fprintf(&b, "\n\n%s %d message", s.messengerTag(ctx, link.UserID, sm.thread), sm.count)
			if sm.count != 1 {
				b.WriteString("s")
			}
			if sm.last != "" {
				b.WriteString(" — last: " + messenger.Clip(sm.last, 160))
			}
		}
		b.WriteString("\n\n/read 3 shows a thread's recent messages.")
		for _, piece := range messenger.Chunk(b.String(), messenger.MaxTextRunes) {
			if _, err := s.messenger.SendText(ctx, link.PSID, piece, nil, ""); err != nil {
				if stop, err := s.relayFailed(ctx, link, err); stop {
					return err
				}
			}
		}
	}
	for _, it := range open {
		err := s.messengerSendQuestion(ctx, link, threadOf(it.question.ThreadID), it.question)
		if stop, err := s.relayFailed(ctx, link, err); stop {
			return err
		}
	}
	// Everything fetched is accounted for.
	var lastMsg, lastQ *relayItem
	for i := range items {
		if items[i].message != nil {
			lastMsg = &items[i]
		} else {
			lastQ = &items[i]
		}
	}
	if lastMsg != nil {
		if err := s.advance(ctx, link, *lastMsg); err != nil {
			return err
		}
	}
	if lastQ != nil {
		return s.advance(ctx, link, *lastQ)
	}
	return nil
}
