// Package frame implements the RPC JSON frame of the gateway control plane
// (contract K1 — the WebSocketAPI frame {t,u,a,p,c,m}, restricted to CALL,
// CALLRESULT and CALLERROR).
package frame

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Type is the message type (field t).
type Type int

const (
	Call       Type = 2
	CallResult Type = 3
	CallError  Type = 4
)

// MaxSize is the largest frame accepted, in bytes (K1: 64 KiB → close 1009).
const MaxSize = 64 * 1024

// ErrTooLarge is returned by Parse for a frame above MaxSize.
var ErrTooLarge = errors.New("frame: larger than 64 KiB")

// Frame is one control-plane message.
type Frame struct {
	Type    Type
	ID      string          // u — UUID v4
	Action  string          // a — CALL only
	Payload json.RawMessage // p — object; "{}" when absent
	Code    int             // c — CALLERROR only
	Message string          // m — CALLERROR only
}

type wire struct {
	T *Type           `json:"t"`
	U string          `json:"u"`
	A string          `json:"a,omitempty"`
	P json.RawMessage `json:"p,omitempty"`
	C *int            `json:"c,omitempty"`
	M string          `json:"m,omitempty"`
}

// Parse decodes and validates one frame.
func Parse(b []byte) (Frame, error) {
	if len(b) > MaxSize {
		return Frame{}, ErrTooLarge
	}
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return Frame{}, fmt.Errorf("frame: %w", err)
	}
	if w.T == nil {
		return Frame{}, errors.New("frame: missing t")
	}
	f := Frame{Type: *w.T, ID: w.U, Action: w.A, Message: w.M}
	if !isUUID(f.ID) {
		return Frame{}, errors.New("frame: u is not a UUID")
	}
	switch f.Type {
	case Call:
		if !strings.HasPrefix(f.Action, "/") {
			return Frame{}, errors.New("frame: CALL needs an action starting with /")
		}
	case CallResult:
	case CallError:
		if w.C == nil {
			return Frame{}, errors.New("frame: CALLERROR needs c")
		}
		f.Code = *w.C
	default:
		return Frame{}, fmt.Errorf("frame: type %d not allowed on this channel", f.Type)
	}
	p := bytes.TrimSpace(w.P)
	switch {
	case len(p) == 0 || bytes.Equal(p, []byte("null")):
		f.Payload = json.RawMessage("{}")
	case p[0] == '{':
		f.Payload = p
	default:
		return Frame{}, errors.New("frame: p is not an object")
	}
	return f, nil
}

// Encode serialises the frame for the wire.
func (f Frame) Encode() ([]byte, error) {
	w := wire{U: f.ID}
	t := f.Type
	w.T = &t
	switch f.Type {
	case Call:
		w.A = f.Action
		w.P = f.Payload
	case CallResult:
		w.P = f.Payload
	case CallError:
		c := f.Code
		w.C = &c
		w.M = f.Message
	default:
		return nil, fmt.Errorf("frame: cannot encode type %d", f.Type)
	}
	if len(w.P) == 0 && f.Type != CallError {
		w.P = json.RawMessage("{}")
	}
	return json.Marshal(w)
}

// NewCall builds a CALL with a fresh UUID; payload is marshalled as JSON.
func NewCall(action string, payload any) Frame {
	p, _ := json.Marshal(payload)
	if payload == nil {
		p = []byte("{}")
	}
	return Frame{Type: Call, ID: NewID(), Action: action, Payload: p}
}

// Result builds the CALLRESULT answering f.
func (f Frame) Result(payload any) Frame {
	p, _ := json.Marshal(payload)
	if payload == nil {
		p = []byte("{}")
	}
	return Frame{Type: CallResult, ID: f.ID, Payload: p}
}

// Error builds the CALLERROR answering f.
func (f Frame) Error(code int, msg string) Frame {
	return Frame{Type: CallError, ID: f.ID, Code: code, Message: msg}
}

// NewID returns a random UUID v4 in canonical form.
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}
