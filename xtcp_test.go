package xtcp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"time"
)

type myPacket struct {
	msg string
}

func (p *myPacket) String() string {
	return p.msg
}

// len + msg
type myProtocol struct {
}

func (mp *myProtocol) PackSize(p Packet) int {
	return 4 + len(p.(*myPacket).msg)
}
func (mp *myProtocol) PackTo(p Packet, w io.Writer) (int, error) {
	msgLen := mp.PackSize(p)
	wl := 0
	err := binary.Write(w, binary.BigEndian, uint32(msgLen))
	if err != nil {
		return wl, err
	}

	n, err := w.Write([]byte(p.(*myPacket).msg))
	wl += n
	if err != nil {
		return wl, err
	}

	return wl, nil
}
func (mp *myProtocol) Pack(p Packet) ([]byte, error) {
	len := mp.PackSize(p)
	if len != 0 {
		buf := bytes.NewBuffer(nil)
		_, err := mp.PackTo(p, buf)
		return buf.Bytes(), err
	}
	return nil, errors.New("err pack size")
}
func (mp *myProtocol) Unpack(buf []byte) (Packet, int, error) {
	if len(buf) < 4 {
		return nil, 0, nil
	}
	msgLen := int(binary.BigEndian.Uint32(buf[:4]))
	if len(buf) < msgLen {
		return nil, 0, nil
	}
	msg := string(buf[4:msgLen])
	return &myPacket{msg: msg}, msgLen, nil
}

type myHandler struct {
	name  string
	sends []string
	recvs []string
}

func (h *myHandler) OnEvent(et EventType, c *Conn, p Packet) {
	switch et {
	case EventConnected:
		// send first msg when client connected.
		sendMsg := &myPacket{
			msg: h.name + time.Now().String(),
		}
		c.Send(sendMsg)
	case EventSend:
		msg := p.(*myPacket).msg
		h.sends = append(h.sends, msg)
	case EventRecv:
		msg := p.(*myPacket).msg
		h.recvs = append(h.recvs, msg)
		if len(h.recvs) == 10 {
			c.Stop(StopGracefullyButNotWait)
		} else {

			sendMsg := &myPacket{
				msg: h.name + time.Now().String(),
			}
			c.Send(sendMsg)
		}
	}
}

func TestXTCP(t *testing.T) {
	p := &myProtocol{}
	hs := &myHandler{name: "server - response : "}
	l, err := net.Listen("tcp", ":")
	if err != nil {
		t.Error("listen err : ", err)
		return
	}
	server := NewServer(NewOpts(hs, p))
	go func() {
		server.Serve(l)
	}()

	hc := &myHandler{name: "client - request : "}
	client := NewConn(NewOpts(hc, p))
	clientClosed := make(chan struct{})
	go func() {
		err := client.DialAndServe(l.Addr().String())
		if err != nil {
			t.Error("client dial err : ", err)
		}
		close(clientClosed)
	}()

	<-clientClosed
	server.Stop(StopGracefullyAndWait)

	if !reflect.DeepEqual(hs.sends, hc.recvs) {
		t.Errorf("server send (%v) != client recv (%v)", len(hs.sends), len(hc.recvs))
	}
	if !reflect.DeepEqual(hs.recvs, hc.sends) {
		t.Errorf("client send (%v) != server recv (%v)", len(hc.sends), len(hs.recvs))
	}
}
