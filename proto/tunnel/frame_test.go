package tunnel

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kayaramazan/latch/proto/model"
)

func TestCodecRoundTrip(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ca, cb := NewCodec(a), NewCodec(b)

	go func() {
		_, _ = ca.Send(TypeHello, 0, &Hello{ProtoMin: 1, ProtoMax: 1, AgentID: "ag_x", Token: "t", Capabilities: []string{CapTCP}})
		_, _ = ca.Send(TypePing, 0, &Ping{TS: time.Unix(1, 0).UTC()})
		_ = ca.SendError(2, ErrScope, "nope", false)
	}()

	env, err := cb.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if env.T != TypeHello || env.ID != 1 || env.Re != 0 {
		t.Errorf("envelope = %+v", env)
	}
	var h Hello
	if err := env.Decode(&h); err != nil || h.AgentID != "ag_x" || h.Capabilities[0] != CapTCP {
		t.Errorf("hello = %+v, %v", h, err)
	}
	env, _ = cb.Recv()
	var p Ping
	if env.T != TypePing || env.ID != 2 || env.Decode(&p) != nil || !p.TS.Equal(time.Unix(1, 0)) {
		t.Errorf("ping = %+v", env)
	}
	env, _ = cb.Recv()
	var e Error
	if env.T != TypeError || env.Re != 2 || env.Decode(&e) != nil || e.Code != ErrScope || e.Fatal {
		t.Errorf("error = %+v %+v", env, e)
	}
	if e.Error() != "ERR_SCOPE: nope" {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestCodecRejectsOversizedAndMalformed(t *testing.T) {
	var buf bytes.Buffer
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, MaxFrameSize+1)
	buf.Write(hdr)
	if _, err := NewCodec(&rw{&buf}).Recv(); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("oversized: %v", err)
	}

	buf.Reset()
	binary.BigEndian.PutUint32(hdr, 3)
	buf.Write(hdr)
	buf.WriteString("{x}")
	if _, err := NewCodec(&rw{&buf}).Recv(); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("malformed: %v", err)
	}

	buf.Reset()
	binary.BigEndian.PutUint32(hdr, 2)
	buf.Write(hdr)
	buf.WriteString("{}")
	if _, err := NewCodec(&rw{&buf}).Recv(); err == nil || !strings.Contains(err.Error(), "missing type") {
		t.Errorf("missing type: %v", err)
	}

	buf.Reset()
	binary.BigEndian.PutUint32(hdr, 10)
	buf.Write(hdr)
	buf.WriteString("{\"t\":")
	if _, err := NewCodec(&rw{&buf}).Recv(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("truncated: %v", err)
	}

	buf.Reset()
	if _, err := NewCodec(&rw{&buf}).Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("clean eof: %v", err)
	}

	c := NewCodec(&rw{&bytes.Buffer{}})
	if _, err := c.Send(TypePolicySync, 0, map[string]string{"x": strings.Repeat("a", MaxFrameSize)}); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("send oversized: %v", err)
	}
}

type rw struct{ *bytes.Buffer }

func TestStreamHeaderAndStatus(t *testing.T) {
	var buf bytes.Buffer
	h := &StreamHeader{RouteID: "rt_a", Kind: model.KindHTTP, Hostname: "app.relay.test", ClientIP: "203.0.113.7", ClientPort: 5, TLS: true, StreamID: "s1"}
	if err := WriteStreamHeader(&buf, h); err != nil {
		t.Fatal(err)
	}
	if err := WriteStatus(&buf, StatusDialFailed); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStreamHeader(&buf)
	if err != nil || got.Hostname != h.Hostname || got.ClientPort != 5 || !got.TLS {
		t.Fatalf("header = %+v, %v", got, err)
	}
	st, err := ReadStatus(&buf)
	if err != nil || st != StatusDialFailed || st.String() != "dial_failed" {
		t.Errorf("status = %v, %v", st, err)
	}
	if _, err := ReadStreamHeader(bytes.NewReader([]byte{0, 0})); err == nil {
		t.Error("empty header accepted")
	}
	if _, err := ReadStreamHeader(bytes.NewReader([]byte{0, 2, '{'})); err == nil {
		t.Error("truncated header accepted")
	}
	if Status(9).String() != "unknown" {
		t.Error("unknown status string")
	}
}

func TestNegotiate(t *testing.T) {
	if v, ok := Negotiate(1, 1); !ok || v != 1 {
		t.Errorf("1-1: %d %v", v, ok)
	}
	if v, ok := Negotiate(1, 5); !ok || v != ProtocolVersion {
		t.Errorf("1-5: %d %v", v, ok)
	}
	if _, ok := Negotiate(2, 3); ok {
		t.Error("2-3 should fail")
	}
}

func TestConnString(t *testing.T) {
	ci := &ConnInfo{Host: "Relay.Example.com", Port: 7443, Token: "lt_abc_def", CAPEM: "-----BEGIN CERTIFICATE-----\nAA==\n-----END CERTIFICATE-----\n", Name: "my vps"}
	s := ci.String()
	if !strings.HasPrefix(s, "latch://Relay.Example.com:7443/?") {
		t.Errorf("string = %s", s)
	}
	back, err := ParseConnString(s)
	if err != nil {
		t.Fatal(err)
	}
	if back.Host != "relay.example.com" || back.Port != 7443 || back.Token != ci.Token || back.CAPEM != ci.CAPEM || back.Name != "my vps" {
		t.Errorf("parsed = %+v", back)
	}
	if back.Addr() != "relay.example.com:7443" {
		t.Errorf("addr = %s", back.Addr())
	}

	min, err := ParseConnString("  latch://r.example.com/?token=x ")
	if err != nil || min.Port != DefaultAgentPort || min.CAPEM != "" {
		t.Errorf("minimal = %+v, %v", min, err)
	}
	for _, bad := range []string{"https://r/?token=x", "latch:///?token=x", "latch://r/?name=x", "latch://r:99999/?token=x", "latch://r/?token=x&ca=!!!", "latch://r/?token=x&ca=aGVsbG8"} {
		if _, err := ParseConnString(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func FuzzRecv(f *testing.F) {
	var seed bytes.Buffer
	_, _ = NewCodec(&rw{&seed}).Send(TypeHello, 0, &Hello{ProtoMin: 1, ProtoMax: 1})
	f.Add(seed.Bytes())
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{255, 255, 255, 255, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		c := NewCodec(&rw{bytes.NewBuffer(data)})
		for {
			env, err := c.Recv()
			if err != nil {
				return
			}
			var h Hello
			_ = env.Decode(&h)
		}
	})
}

func FuzzReadStreamHeader(f *testing.F) {
	var seed bytes.Buffer
	_ = WriteStreamHeader(&seed, &StreamHeader{RouteID: "rt_a"})
	f.Add(seed.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ReadStreamHeader(bytes.NewReader(data))
	})
}
