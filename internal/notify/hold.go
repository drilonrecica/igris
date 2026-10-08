package notify

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/drilonrecica/igris/internal/textsafe"
)

// queue is what the router holds back for one channel.
type queue struct {
	held  []heldMessage // quiet hours, oldest first
	tasks []Message     // task_done messages collected for a digest
}

// heldMessage is a message held in quiet hours and when it was held.
type heldMessage struct {
	m  Message
	at time.Time
}

// step is one thing a channel does for one call: send m, or hold it.
type step struct {
	m    Message
	hold bool
}

// job is a channel's steps for one call, done in order.
type job struct {
	ch    Channel
	steps []step
	// once sends each step in a single attempt, without the retry.
	once bool
}

// finalFlushTimeout bounds FlushAll as a whole: igris is exiting, and a
// dead server must not keep it from doing so.
const finalFlushTimeout = 15 * time.Second

// Digest limits (SPEC §10).
const (
	maxDigestLines = 20
	maxDigestIDs   = 10
)

// route decides what each channel does with m.
func (r *Router) route(m Message) []job {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.o.Now()
	var jobs []job
	for i, e := range r.o.Channels {
		if !e.wants(m.Event) {
			continue
		}
		msg := m
		if m.Event == TaskDone && r.o.TaskDoneDigest.On() {
			q := &r.queues[i]
			q.tasks = append(q.tasks, m)
			n := r.o.TaskDoneDigest.Every()
			if n == 0 || len(q.tasks) < n {
				continue
			}
			msg = r.takeTasks(i)
		}
		jobs = append(jobs, job{ch: e.Channel, steps: r.pass(i, msg, now)})
	}
	return jobs
}

// pass is what channel i does with msg at now: in quiet hours it holds
// what doesn't break through; outside them it first sends the digest of
// what it held, if any. Called with r.mu held.
func (r *Router) pass(i int, msg Message, now time.Time) []step {
	q := &r.queues[i]
	quiet := r.quiet(now)
	var steps []step
	if !quiet && len(q.held) > 0 {
		steps = append(steps, step{m: r.takeDigest(i, now)})
	}
	if quiet && !r.o.Channels[i].NeverHeld && !slices.Contains(r.o.BreakThrough, msg.Event) {
		q.held = append(q.held, heldMessage{m: msg, at: now})
		return append(steps, step{m: msg, hold: true})
	}
	return append(steps, step{m: msg})
}

func (r *Router) quiet(now time.Time) bool { return r.o.Quiet != nil && r.o.Quiet.Contains(now) }

// Flush sends each channel the digest of what it held, once quiet hours
// are over; inside them it does nothing. The run loop calls it as its
// clock moves, also while it waits on the owner.
func (r *Router) Flush(ctx context.Context) []Result {
	r.mu.Lock()
	now := r.o.Now()
	var jobs []job
	if !r.quiet(now) {
		for i := range r.queues {
			if len(r.queues[i].held) > 0 {
				jobs = append(jobs, job{ch: r.o.Channels[i].Channel, steps: []step{{m: r.takeDigest(i, now)}}})
			}
		}
	}
	r.mu.Unlock()
	return r.collect(ctx, jobs)
}

// FlushTasks sends the task_done messages collected for a digest, one
// message per channel, at the end of a phase. In quiet hours that message
// is held like any task_done.
func (r *Router) FlushTasks(ctx context.Context) []Result {
	return r.flush(ctx, false)
}

// FlushAll is for the end of a run: it sends the collected task_done
// messages and then, whatever the time, the digest of everything held,
// since nothing can hold them once igris exits. Each message gets one
// attempt, no retry, and all of it at most finalFlushTimeout.
func (r *Router) FlushAll(ctx context.Context) []Result {
	ctx, cancel := context.WithTimeout(ctx, finalFlushTimeout)
	defer cancel()
	return r.flush(ctx, true)
}

func (r *Router) flush(ctx context.Context, all bool) []Result {
	r.mu.Lock()
	now := r.o.Now()
	var jobs []job
	for i := range r.queues {
		var steps []step
		if len(r.queues[i].tasks) > 0 {
			steps = r.pass(i, r.takeTasks(i), now)
		}
		if all && len(r.queues[i].held) > 0 {
			steps = append(steps, step{m: r.takeDigest(i, now)})
		}
		if len(steps) > 0 {
			jobs = append(jobs, job{ch: r.o.Channels[i].Channel, steps: steps, once: all})
		}
	}
	r.mu.Unlock()
	return r.collect(ctx, jobs)
}

// takeTasks empties channel i's collected task_done messages into one
// message "N tasks done: ID, ID, …" (no task ID or title, so templates
// apply). A single one is sent as it is. Called with r.mu held.
func (r *Router) takeTasks(i int) Message {
	tasks := r.queues[i].tasks
	r.queues[i].tasks = nil
	last := tasks[len(tasks)-1]
	if len(tasks) == 1 {
		return last
	}
	ids := make([]string, 0, maxDigestIDs+1)
	for k, t := range tasks {
		if k == maxDigestIDs {
			ids = append(ids, "…")
			break
		}
		ids = append(ids, t.TaskID)
	}
	return Message{
		Event:   TaskDone,
		Project: last.Project,
		Phase:   last.Phase,
		What:    strconv.Itoa(len(tasks)) + " tasks done: " + strings.Join(ids, ", "),
		RunID:   last.RunID,
		At:      last.At,
	}
}

// takeDigest empties channel i's held messages into one digest. Called
// with r.mu held.
func (r *Router) takeDigest(i int, now time.Time) Message {
	held := r.queues[i].held
	r.queues[i].held = nil
	window := ""
	if r.o.Quiet != nil {
		window = " " + r.o.Quiet.String()
	}
	lines := []string{fmt.Sprintf("quiet hours%s: %d held", window, len(held))}
	msgs := make([]Message, len(held))
	for k, h := range held {
		msgs[k] = h.m
		if k < maxDigestLines {
			lines = append(lines, h.at.Format("15:04")+" "+string(h.m.Event)+": "+textsafe.Line(h.m.Body()))
		}
	}
	if more := len(held) - maxDigestLines; more > 0 {
		lines = append(lines, fmt.Sprintf("… and %d more", more))
	}
	last := held[len(held)-1].m
	return Message{
		Event:   Digest,
		Project: last.Project,
		What:    strings.Join(lines, "\n"),
		RunID:   last.RunID,
		At:      now,
		Held:    msgs,
	}
}
