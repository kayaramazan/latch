package tunnel

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Frame limits from PROTOCOL.md.
const (
	MaxFrameSize        = 1 << 20 // control frames
	MaxStreamHeaderSize = 1<<16 - 1
)

// ErrFrameTooLarge is returned when a peer sends an oversized frame.
var ErrFrameTooLarge = errors.New("tunnel: frame exceeds 1 MiB")

// Envelope wraps every control message.
type Envelope struct {
	T  Type            `json:"t"`
	ID uint64          `json:"id"`
	Re uint64          `json:"re,omitempty"`
	D  json.RawMessage `json:"d,omitempty"`
}

// Decode unmarshals the body into v.
func (e *Envelope) Decode(v any) error {
	if len(e.D) == 0 {
		return nil
	}
	if err := json.Unmarshal(e.D, v); err != nil {
		return fmt.Errorf("tunnel: decode %s: %w", e.T, err)
	}
	return nil
}

// Codec reads and writes framed envelopes on a control stream. Send is safe
// for concurrent use; Recv must be called from one goroutine.
type Codec struct {
	r      *bufio.Reader
	w      io.Writer
	wmu    sync.Mutex
	nextID atomic.Uint64
}

// NewCodec wraps a control stream.
func NewCodec(rw io.ReadWriter) *Codec {
	return &Codec{r: bufio.NewReaderSize(rw, 64<<10), w: rw}
}

// Send encodes body and writes one frame. re is the id being answered (0 for
// none). It returns the new message id.
func (c *Codec) Send(t Type, re uint64, body any) (uint64, error) {
	var raw json.RawMessage
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("tunnel: encode %s: %w", t, err)
		}
		raw = b
	}
	id := c.nextID.Add(1)
	frame, err := json.Marshal(Envelope{T: t, ID: id, Re: re, D: raw})
	if err != nil {
		return 0, err
	}
	if len(frame) > MaxFrameSize {
		return 0, ErrFrameTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(frame)))
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(hdr[:]); err != nil {
		return 0, err
	}
	if _, err := c.w.Write(frame); err != nil {
		return 0, err
	}
	return id, nil
}

// SendError is a convenience for Error frames.
func (c *Codec) SendError(re uint64, code, message string, fatal bool) error {
	_, err := c.Send(TypeError, re, &Error{Code: code, Message: message, Fatal: fatal})
	return err
}

// Recv reads one frame. It returns ErrFrameTooLarge for oversized frames
// (the connection should then be closed) and io.EOF on clean close.
func (c *Codec) Recv() (*Envelope, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrameSize {
		return nil, ErrFrameTooLarge
	}
	if n == 0 {
		return nil, fmt.Errorf("tunnel: empty frame")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.r, buf); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	var env Envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		return nil, fmt.Errorf("tunnel: malformed frame: %w", err)
	}
	if env.T == "" {
		return nil, fmt.Errorf("tunnel: malformed frame: missing type")
	}
	return &env, nil
}

// WriteStreamHeader writes the u16-length-prefixed header on a data stream.
func WriteStreamHeader(w io.Writer, h *StreamHeader) error {
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if len(b) > MaxStreamHeaderSize {
		return fmt.Errorf("tunnel: stream header too large")
	}
	buf := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(buf, uint16(len(b)))
	copy(buf[2:], b)
	_, err = w.Write(buf)
	return err
}

// ReadStreamHeader reads a header written by WriteStreamHeader.
func ReadStreamHeader(r io.Reader) (*StreamHeader, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	if n == 0 {
		return nil, fmt.Errorf("tunnel: empty stream header")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var h StreamHeader
	if err := json.Unmarshal(buf, &h); err != nil {
		return nil, fmt.Errorf("tunnel: malformed stream header: %w", err)
	}
	return &h, nil
}

// WriteStatus writes the agent's one-byte stream status.
func WriteStatus(w io.Writer, s Status) error {
	_, err := w.Write([]byte{byte(s)})
	return err
}

// ReadStatus reads the agent's one-byte stream status.
func ReadStatus(r io.Reader) (Status, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return Status(b[0]), nil
}
