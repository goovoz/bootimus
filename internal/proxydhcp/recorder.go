package proxydhcp

import (
	"sync"
	"time"
)

const (
	OutcomeReplied = "replied"
	OutcomeIgnored = "ignored"
	OutcomeFailed  = "failed"

	DefaultRecorderSize = 500
)

type Event struct {
	Time                time.Time `json:"time"`
	Port                int       `json:"port"`
	Source              string    `json:"source"`
	Relay               string    `json:"relay,omitempty"`
	MAC                 string    `json:"mac"`
	MessageType         string    `json:"message_type"`
	Arch                int       `json:"arch"`
	ArchName            string    `json:"arch_name"`
	VendorClass         string    `json:"vendor_class"`
	ClientGUID          string    `json:"client_guid,omitempty"`
	Outcome             string    `json:"outcome"`
	Decision            string    `json:"decision"`
	Bootfile            string    `json:"bootfile,omitempty"`
	ReplyType           string    `json:"reply_type,omitempty"`
	ReplyTo             string    `json:"reply_to,omitempty"`
	VendorOptionOmitted bool      `json:"vendor_option_omitted"`
	RaspberryPi         bool      `json:"raspberry_pi"`
}

type Recorder struct {
	mu          sync.RWMutex
	events      []Event
	max         int
	subscribers map[chan Event]bool
}

func NewRecorder(size int) *Recorder {
	if size <= 0 {
		size = DefaultRecorderSize
	}
	return &Recorder{
		events:      make([]Event, 0, size),
		max:         size,
		subscribers: make(map[chan Event]bool),
	}
}

func (r *Recorder) Record(ev Event) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	if len(r.events) > r.max {
		r.events = r.events[len(r.events)-r.max:]
	}
	for ch := range r.subscribers {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (r *Recorder) Events() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

func (r *Recorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = r.events[:0]
}

func (r *Recorder) Subscribe() chan Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan Event, 32)
	r.subscribers[ch] = true
	return ch
}

func (r *Recorder) Unsubscribe(ch chan Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.subscribers[ch] {
		delete(r.subscribers, ch)
		close(ch)
	}
}
