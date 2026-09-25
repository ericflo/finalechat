package api

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ericflo/finalechat/internal/bus"
	"github.com/ericflo/finalechat/internal/control"
	"github.com/ericflo/finalechat/internal/messenger"
	"github.com/ericflo/finalechat/internal/store"
)

// command runs a connector command. It reports false for anything it does
// not own, which then goes to the agent as text (eagent has slash commands
// of its own).
func (c *mchat) command() (bool, error) {
	word, args, _ := strings.Cut(c.in.text, " ")
	args = strings.TrimSpace(args)
	switch strings.ToLower(word) {
	case "/help", "/?", "/commands":
		c.say(messengerHelp, nil)
	case "/ls", "/threads", "/list":
		return true, c.list()
	case "/go", "/use", "/focus":
		if args == "" {
			if err := c.s.store.SetMessengerPin(c.ctx, c.link.UserID, nil); err != nil {
				return true, err
			}
			c.say("Replies now go to whichever thread spoke last.", nil)
			return true, nil
		}
		n, err := strconv.Atoi(strings.TrimPrefix(args, "#"))
		if err != nil {
			c.say("Use /go with a thread number from /ls, like /go 3.", nil)
			return true, nil
		}
		return true, c.goTo(n)
	case "/where", "/here":
		return true, c.where()
	case "/read":
		return true, c.read(args)
	case "/new":
		return true, c.newSession(args)
	case "/cancel":
		c.setNew(nil)
		c.say("Cancelled.", nil)
	case "/skip", "/dismiss":
		return true, c.skip()
	case "/more":
		return true, c.more()
	case "/quiet":
		if err := c.s.store.SetMessengerImportantOnly(c.ctx, c.link.UserID, true); err != nil {
			return true, err
		}
		c.say("Quiet: only questions and important messages arrive here. /loud for everything.", nil)
	case "/loud":
		if err := c.s.store.SetMessengerImportantOnly(c.ctx, c.link.UserID, false); err != nil {
			return true, err
		}
		c.say("Every agent message arrives here again.", nil)
	case "/remote":
		return true, c.remote(strings.ToLower(args))
	case "/unlink":
		c.say("Unlinked. Link again from Finalechat → Settings → Messenger.", nil)
		return true, c.s.store.DeleteMessengerLink(c.ctx, c.link.UserID)
	case "/link":
		c.say("This chat is already linked. /unlink first to link another account.", nil)
	case "/quit", "/exit", "/q":
		c.say("That would stop the agent's session. Send /quit! to really stop it.", nil)
	case "/quit!":
		return true, c.reply("/quit")
	default:
		return false, nil
	}
	return true, nil
}

func (c *mchat) goTo(n int) error {
	id, err := c.s.store.MessengerThreadByHandle(c.ctx, c.link.UserID, n)
	if errors.Is(err, store.ErrNotFound) {
		c.say(fmt.Sprintf("There is no thread #%d. /ls lists them.", n), nil)
		return nil
	}
	if err != nil {
		return err
	}
	thread, err := c.s.store.GetThread(c.ctx, c.user.ID, id)
	if err != nil {
		return err
	}
	if err := c.s.store.SetMessengerPin(c.ctx, c.link.UserID, &id); err != nil {
		return err
	}
	_ = c.s.store.SetMessengerLastThread(c.ctx, c.link.UserID, id)
	c.sayAbout(id, "📌 Replies go to "+c.s.messengerTag(c.ctx, c.link.UserID, thread)+" until you send /go on its own.", nil)
	return nil
}

func (c *mchat) list() error {
	threads, _, err := c.s.store.ListThreads(c.ctx, c.user.ID, store.ThreadFilter{Limit: 10})
	if err != nil {
		return err
	}
	if len(threads) == 0 {
		c.say("No threads yet. /new starts a session.", nil)
		return nil
	}
	target := c.link.Target()
	var b strings.Builder
	var quick []messenger.QuickReply
	for _, t := range threads {
		h, err := c.s.store.MessengerHandle(c.ctx, c.link.UserID, t.ID)
		if err != nil {
			return err
		}
		tag := c.s.messengerTag(c.ctx, c.link.UserID, t)
		marker := ""
		if target != nil && *target == t.ID {
			marker = " ←"
		}
		fmt.Fprintf(&b, "%s%s\n", strings.Trim(tag, "[]"), marker)
		var line string
		switch {
		case t.Activity != nil:
			line = "⋯ " + t.Activity.Text + " (" + ago(t.Activity.Since) + ")"
		case t.Preview != "":
			who := ""
			if t.PreviewSender == store.SenderUser {
				who = "you: "
			}
			line = who + t.Preview + " (" + ago(t.LastActivityAt) + ")"
		}
		if t.PendingQuestions > 0 {
			line = "❓ " + line
		}
		if line != "" {
			b.WriteString("   " + messenger.Clip(line, 90) + "\n")
		}
		if len(quick) < 10 {
			quick = append(quick, messenger.QuickReply{Title: "#" + strconv.Itoa(h) + " " + t.Title, Payload: "go:" + strconv.Itoa(h)})
		}
	}
	mode := "Replies follow whoever spoke last."
	if c.link.PinnedThreadID != nil {
		mode = "Replies are pinned (←); /go alone to follow again."
	}
	c.say(strings.TrimSpace(b.String())+"\n\n"+mode, quick)
	return nil
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d"
}

func (c *mchat) where() error {
	target := c.link.Target()
	if target == nil {
		c.say("No current thread. /ls lists threads.", nil)
		return nil
	}
	thread, err := c.s.store.GetThread(c.ctx, c.user.ID, *target)
	if err != nil {
		c.say("The current thread is gone. /ls lists threads.", nil)
		return nil
	}
	var b strings.Builder
	b.WriteString(c.s.messengerTag(c.ctx, c.link.UserID, thread))
	if c.link.PinnedThreadID != nil {
		b.WriteString(" 📌")
	}
	if thread.Description != "" {
		b.WriteString("\n" + messenger.Clip(thread.Description, 300))
	}
	if thread.Activity != nil {
		b.WriteString("\n⋯ " + thread.Activity.Text + " (for " + ago(thread.Activity.Since) + ")")
	} else {
		b.WriteString("\nNo live status; last activity " + ago(thread.LastActivityAt) + " ago.")
	}
	if cwd, ok := thread.Meta["cwd"].(string); ok && cwd != "" {
		b.WriteString("\n📁 " + cwd)
	}
	q, err := c.pendingQuestion(thread.ID)
	if err != nil {
		return err
	}
	if q != nil {
		b.WriteString("\n\n❓ " + messenger.Clip(q.Prompt, 600))
		for i, o := range q.Options {
			fmt.Fprintf(&b, "\n%d. %s", i+1, o.Label)
		}
	}
	c.sayAbout(thread.ID, b.String(), nil)
	return nil
}

func (c *mchat) read(args string) error {
	var threadID *uuid.UUID
	if args != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(args, "#"))
		if err != nil {
			c.say("Use /read with a thread number, like /read 3.", nil)
			return nil
		}
		id, err := c.s.store.MessengerThreadByHandle(c.ctx, c.link.UserID, n)
		if err != nil {
			c.say(fmt.Sprintf("There is no thread #%d. /ls lists them.", n), nil)
			return nil
		}
		threadID = &id
	} else {
		threadID = c.link.Target()
	}
	if threadID == nil {
		c.say("No current thread. /read 3 reads #3.", nil)
		return nil
	}
	thread, err := c.s.store.GetThread(c.ctx, c.user.ID, *threadID)
	if err != nil {
		c.say("That thread is gone.", nil)
		return nil
	}
	msgs, _, _, err := c.s.store.ListMessages(c.ctx, c.user.ID, thread.ID, store.MessagePage{Limit: 6})
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(c.s.messengerTag(c.ctx, c.link.UserID, thread) + " recent:")
	for _, m := range msgs {
		who := "agent"
		switch m.Sender {
		case store.SenderUser:
			who = "you"
		case store.SenderSystem:
			who = "·"
		}
		body := m.Body
		if m.Format != "text" {
			body = messenger.Markdown(body)
		}
		fmt.Fprintf(&b, "\n\n%s (%s): %s", who, ago(m.CreatedAt), messenger.Clip(strings.TrimSpace(body), 400))
	}
	for i, piece := range messenger.Chunk(b.String(), messenger.MaxTextRunes) {
		if i == 2 {
			break
		}
		c.sayAbout(thread.ID, piece, nil)
	}
	return nil
}

func (c *mchat) skip() error {
	target := c.link.Target()
	if target == nil {
		c.say("No current thread.", nil)
		return nil
	}
	thread, err := c.s.store.GetThread(c.ctx, c.user.ID, *target)
	if err != nil {
		c.say("The current thread is gone.", nil)
		return nil
	}
	q, err := c.pendingQuestion(thread.ID)
	if err != nil {
		return err
	}
	if q == nil {
		c.sayAbout(thread.ID, c.s.messengerTag(c.ctx, c.link.UserID, thread)+" has no open question.", nil)
		return nil
	}
	return c.dismiss(thread, q)
}

func (c *mchat) more() error {
	st, _ := c.link.State["more"].(map[string]any)
	idRaw, _ := st["message_id"].(string)
	next, _ := st["next"].(float64)
	id, err := uuid.Parse(idRaw)
	if err != nil {
		c.say("Nothing more to show.", nil)
		return nil
	}
	msg, err := c.s.store.GetMessage(c.ctx, c.user.ID, id)
	if err != nil {
		c.say("That message is gone.", nil)
		return nil
	}
	thread, err := c.s.store.GetThread(c.ctx, c.user.ID, msg.ThreadID)
	if err != nil {
		return err
	}
	_, err = c.s.messengerSendPieces(c.ctx, c.link, thread, msg, int(next))
	return err
}

func (c *mchat) remote(arg string) error {
	settings := c.user.Settings
	switch arg {
	case "on":
		settings.RemoteMode = true
	case "off":
		settings.RemoteMode = false
	default:
		state := "off"
		if settings.RemoteMode {
			state = "on"
		}
		c.say("Remote mode is "+state+". /remote on makes Claude Code wait for your replies here; /remote off hands it back to the terminal.", nil)
		return nil
	}
	if _, err := c.s.store.UpdateUserSettings(c.ctx, c.user.ID, settings); err != nil {
		return err
	}
	c.s.bus.Publish(context.WithoutCancel(c.ctx), bus.Event{Type: bus.SettingsUpdated, UserID: c.user.ID.String()})
	c.say("Remote mode "+arg+".", nil)
	return nil
}

// Starting a session is a short conversation: which project (when more than
// one eagent is connected), which directory, then the prompt. "/new <prompt>"
// skips the directory and starts in the project root.

type messengerNew struct {
	Step     string    `json:"step"` // project, dir, prompt
	Resource uuid.UUID `json:"resource"`
	Cwd      string    `json:"cwd,omitempty"`
	Dirs     []string  `json:"dirs,omitempty"`
	Prompt   string    `json:"prompt,omitempty"`
	Starters []string  `json:"starters,omitempty"`
}

func (c *mchat) newState() *messengerNew {
	raw, ok := c.link.State["new"].(map[string]any)
	if !ok {
		return nil
	}
	var st messengerNew
	if err := remarshal(raw, &st); err != nil || st.Step == "" {
		return nil
	}
	return &st
}

func (c *mchat) setNew(st *messengerNew) {
	state := store.JSON{}
	for k, v := range c.link.State {
		state[k] = v
	}
	if st == nil {
		delete(state, "new")
	} else {
		state["new"] = st
	}
	c.link.State = state
	if err := c.s.store.SetMessengerState(c.ctx, c.link.UserID, state); err != nil {
		c.s.log.Warn("messenger state", "err", err)
	}
}

type messengerStarter struct {
	resource  *store.SettingsResource
	connector *store.Connector
}

func (s *Server) messengerStarters(ctx context.Context, userID uuid.UUID) ([]messengerStarter, error) {
	connectors, err := s.store.ListConnectors(ctx, userID)
	if err != nil {
		return nil, err
	}
	var out []messengerStarter
	for _, conn := range connectors {
		if conn.State != "active" {
			continue
		}
		resources, err := s.store.ListSettingsResources(ctx, userID, conn.ID)
		if err != nil {
			return nil, err
		}
		for _, r := range resources {
			for _, a := range r.Descriptor.Actions {
				if a.Operation == "session.start" {
					out = append(out, messengerStarter{resource: r, connector: conn})
					break
				}
			}
		}
	}
	return out, nil
}

// directories lists where a session may start: the root first, then the
// integration's recent directories.
func directories(r *store.SettingsResource) (string, []string) {
	root := r.Snapshot.Context
	raw, _ := r.Snapshot.Details["directories"].(map[string]any)
	if v, ok := raw["root"].(string); ok && v != "" {
		root = v
	}
	dirs := []string{root}
	seen := map[string]bool{root: true}
	for _, key := range []string{"recent", "children"} {
		list, _ := raw[key].([]any)
		for _, v := range list {
			if d, ok := v.(string); ok && d != "" && !seen[d] && len(dirs) < 10 {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
	}
	return root, dirs
}

var homePrefixRe = regexp.MustCompile(`^/(?:home|Users)/[^/]+`)

func shortDir(p string) string { return homePrefixRe.ReplaceAllString(p, "~") }

func (c *mchat) newSession(prompt string) error {
	starters, err := c.s.messengerStarters(c.ctx, c.user.ID)
	if err != nil {
		return err
	}
	if len(starters) == 0 {
		c.say("No eagent is connected. On the machine, run `eagent connector run` (or `eagent serve`) in the project, then try /new again.", nil)
		return nil
	}
	if len(starters) == 1 {
		return c.chooseProject(starters[0], prompt)
	}
	var b strings.Builder
	b.WriteString("Which project?")
	var quick []messenger.QuickReply
	ids := make([]string, 0, len(starters))
	for i, st := range starters {
		online := ""
		if !st.connector.Online() {
			online = " (offline)"
		}
		fmt.Fprintf(&b, "\n%d. %s%s", i+1, st.resource.Label, online)
		ids = append(ids, st.resource.ID.String())
		if len(quick) < messenger.MaxQuickReplies {
			quick = append(quick, messenger.QuickReply{Title: strconv.Itoa(i+1) + " " + st.resource.Label, Payload: "np:" + st.resource.ID.String()})
		}
	}
	c.setNew(&messengerNew{Step: "project", Prompt: prompt, Starters: ids})
	c.say(b.String()+"\n\n/cancel to stop.", quick)
	return nil
}

func (c *mchat) chooseProject(st messengerStarter, prompt string) error {
	root, dirs := directories(st.resource)
	if !st.connector.Online() {
		c.say(st.resource.Label+" is offline. Start `eagent connector run` (or `eagent serve`) there, then try again.", nil)
		c.setNew(nil)
		return nil
	}
	if prompt != "" {
		return c.startSession(st.resource.ID, root, prompt)
	}
	if len(dirs) <= 1 {
		c.setNew(&messengerNew{Step: "prompt", Resource: st.resource.ID, Cwd: root})
		c.say("📁 "+shortDir(root)+"\nWhat should the session do? (/cancel to stop)", nil)
		return nil
	}
	var b strings.Builder
	b.WriteString("Where should it start? Pick a number or send a path.")
	var quick []messenger.QuickReply
	for i, d := range dirs {
		fmt.Fprintf(&b, "\n%d. %s", i+1, shortDir(d))
		quick = append(quick, messenger.QuickReply{Title: strconv.Itoa(i+1) + " " + path.Base(d), Payload: "nd:" + strconv.Itoa(i)})
	}
	c.setNew(&messengerNew{Step: "dir", Resource: st.resource.ID, Dirs: dirs})
	c.say(b.String(), quick)
	return nil
}

// newChoice handles a tapped project ("np") or directory ("nd").
func (c *mchat) newChoice(kind, value string) error {
	st := c.newState()
	if st == nil {
		c.say("That choice has expired. Send /new to start again.", nil)
		return nil
	}
	if kind == "np" {
		id, err := uuid.Parse(value)
		if err != nil {
			return nil
		}
		return c.pickProject(st, id)
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || n >= len(st.Dirs) {
		return nil
	}
	return c.pickDir(st, st.Dirs[n])
}

func (c *mchat) pickProject(st *messengerNew, id uuid.UUID) error {
	starters, err := c.s.messengerStarters(c.ctx, c.user.ID)
	if err != nil {
		return err
	}
	for _, s := range starters {
		if s.resource.ID == id {
			return c.chooseProject(s, st.Prompt)
		}
	}
	c.say("That project is no longer connected. Send /new to see what is.", nil)
	c.setNew(nil)
	return nil
}

func (c *mchat) pickDir(st *messengerNew, dir string) error {
	c.setNew(&messengerNew{Step: "prompt", Resource: st.Resource, Cwd: dir})
	c.say("📁 "+shortDir(dir)+"\nWhat should the session do? (/cancel to stop)", nil)
	return nil
}

// newTyped handles typed answers while a /new conversation waits for a
// project or directory choice. It reports false when the text is not one.
func (c *mchat) newTyped(text string) (bool, error) {
	st := c.newState()
	if st == nil || (st.Step != "project" && st.Step != "dir") {
		return false, nil
	}
	if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
		switch {
		case st.Step == "project" && n >= 1 && n <= len(st.Starters):
			id, _ := uuid.Parse(st.Starters[n-1])
			return true, c.pickProject(st, id)
		case st.Step == "dir" && n >= 1 && n <= len(st.Dirs):
			return true, c.pickDir(st, st.Dirs[n-1])
		}
		c.say("Pick one of the numbers above, or /cancel.", nil)
		return true, nil
	}
	if st.Step == "dir" && (strings.HasPrefix(text, "/") || strings.HasPrefix(text, "~")) {
		dir := strings.TrimSpace(text)
		if strings.HasPrefix(dir, "~") && len(st.Dirs) > 0 {
			if home := homePrefixRe.FindString(st.Dirs[0]); home != "" {
				dir = home + strings.TrimPrefix(dir, "~")
			}
		}
		return true, c.pickDir(st, path.Clean(dir))
	}
	return false, nil
}

func (c *mchat) startSession(resourceID uuid.UUID, cwd, prompt string) error {
	var cmd *store.SettingsCommand
	var created bool
	var r *store.SettingsResource
	for attempt := 0; attempt < 2; attempt++ {
		var err error
		r, err = c.s.store.GetSettingsResource(c.ctx, c.user.ID, resourceID)
		if errors.Is(err, store.ErrNotFound) {
			c.setNew(nil)
			c.say("That project is no longer connected. Send /new to see what is.", nil)
			return nil
		}
		if err != nil {
			return err
		}
		root, _ := directories(r)
		params := map[string]any{"prompt": prompt}
		if cwd != "" && cwd != root {
			params["cwd"] = cwd
		}
		proposal := control.Proposal{Operation: "session.start", SchemaVersion: r.Descriptor.SchemaVersion, ExpectedVersion: r.Snapshot.Version, Generation: r.Generation, Parameters: params}
		// The key includes the attempt: a retry after a version race is a
		// different proposal, which the idempotency check would refuse.
		cmd, created, err = c.s.store.CreateSettingsCommand(c.ctx, c.user.ID, r.ID, nil, nil, nil, "messenger:"+c.in.mid+":"+strconv.Itoa(attempt), proposal, time.Now().Add(5*time.Minute), false)
		switch {
		case err == nil:
		case errors.Is(err, store.ErrConflict) && attempt == 0:
			continue // the integration republished its settings meanwhile
		case errors.Is(err, store.ErrConnectorOffline):
			c.setNew(nil)
			c.say(r.Label+" is offline. Start `eagent connector run` (or `eagent serve`) there, then /new again.", nil)
			return nil
		case errors.Is(err, store.ErrInvalidState), errors.Is(err, store.ErrConflict):
			c.setNew(nil)
			c.say("eagent refused the session: "+strings.TrimPrefix(err.Error(), "invalid state: "), nil)
			return nil
		default:
			return err
		}
		break
	}
	if created {
		c.s.controlEvent(context.WithoutCancel(c.ctx), c.user.ID, cmd.ConnectorID, cmd.ResourceID, cmd.ID, bus.CommandUpdated)
	}
	c.setNew(nil)
	dir := cwd
	if dir == "" {
		dir, _ = directories(r)
	}
	c.say("Starting a session in "+shortDir(dir)+"…", nil)
	go c.s.messengerFollowStart(c.link.UserID, c.link.PSID, cmd.ID, dir)
	return nil
}

// messengerFollowStart waits for the integration to run a session.start
// command and for the session's thread to appear, then points the chat at it.
func (s *Server) messengerFollowStart(userID uuid.UUID, psid string, commandID uuid.UUID, dir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var cmd *store.SettingsCommand
	for {
		var err error
		cmd, err = s.store.GetSettingsCommand(ctx, userID, commandID)
		if err != nil {
			return
		}
		if cmd.Status != "queued" && cmd.Status != "executing" {
			break
		}
		if !sleepCtx(ctx, s.messengerPoll) {
			s.messengerSay(context.Background(), psid, "eagent has not picked up the session yet. It may still start; /ls shows new threads.", nil)
			return
		}
	}
	if cmd.Status != "succeeded" {
		msg, _ := cmd.Result["message"].(string)
		if msg == "" {
			msg = "eagent did not start the session (" + cmd.Status + ")."
		}
		s.messengerSay(ctx, psid, msg, nil)
		return
	}
	ref, _ := cmd.Result["thread"].(string)
	ext, ok := strings.CutPrefix(ref, "ext:")
	if !ok || ext == "" {
		s.messengerSay(ctx, psid, "The session started. /ls shows its thread once it appears.", nil)
		return
	}
	for {
		thread, err := s.store.GetThreadByExternalID(ctx, userID, ext)
		if err == nil {
			_ = s.store.SetMessengerLastThread(ctx, userID, thread.ID)
			note := ""
			if interactive, ok := cmd.Result["interactive"].(bool); ok && !interactive {
				note = " as a batch run"
			}
			text := "▶ Started " + s.messengerTag(ctx, userID, thread) + note + " in " + shortDir(dir) + ". Replies go there."
			if mid, err := s.messenger.SendText(ctx, psid, text, nil, ""); err == nil {
				_ = s.store.RecordMessengerSent(ctx, userID, mid, &thread.ID, nil)
			}
			return
		}
		if !sleepCtx(ctx, s.messengerPoll) {
			s.messengerSay(context.Background(), psid, "The session started, but its thread has not appeared yet. /ls will show it.", nil)
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
