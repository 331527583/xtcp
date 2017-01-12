package xtcp

import (
	"github.com/xfxdev/xlog"
	"net"
	"sync"
	"time"
)

var (
	// DefaultSendBufLength is the default length of send buf per connection.
	DefaultSendBufLength uint = 128
)

// Handler is the interface of tcp server callback.
type Handler interface {
	OnConnected(c *Conn)
	OnRecv(p Package)
	OnClosed(c *Conn)
}

// ServerOpts if the options of server.
type ServerOpts struct {
	LisAddr    string
	Handler    Handler
	Protocol   Protocol
	SendBufLen uint // default is DefaultSendBufLength.
}

// Server used for running a tcp server.
type Server struct {
	Opts  *ServerOpts
	stop  chan struct{}
	wg    sync.WaitGroup
	mu    sync.Mutex
	lis   net.Listener
	conns map[*Conn]bool
}

// Serve start the tcp server to accept.
func (s *Server) Serve() {
	defer func() {
		s.wg.Done()

		s.mu.Lock()
		lis := s.lis
		s.lis = nil
		s.mu.Unlock()

		if lis != nil {
			lis.Close()
		}
	}()

	s.wg.Add(1)

	l, err := net.Listen("tcp", s.Opts.LisAddr)
	if err != nil {
		xlog.Fatalf("XTCP server: listen error: %v, addr: %v", err, s.Opts.LisAddr)
		return
	}

	xlog.Info("XTCP server: listen on: ", l.Addr().String())

	s.mu.Lock()
	s.lis = l
	s.mu.Unlock()

	var tempDelay time.Duration // how long to sleep on accept failure

	for {
		conn, err := l.Accept()
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				xlog.Errorf("XTCP Server: Accept error: %v; retrying in %v", err, tempDelay)
				select {
				case <-time.After(tempDelay):
					continue
				case <-s.stop:
					return
				}
			}

			select {
			case <-s.stop:
				// don't log if listener closed.
			default:
				xlog.Errorf("XTCP Server: Accept error: %v; server closed!", err)
			}

			return
		}

		tempDelay = 0
		go s.handleRawConn(conn)
	}
}

// Stop stops the tcp server.
// StopImmediately: immediately closes all open connections and listener.
// StopGracefullyButNotWait: stops the server to accept new connections.
// StopGracefullyAndWait: stops the server to accept new connections and blocks until all connections are closed.
func (s *Server) Stop(mode StopMode) {
	close(s.stop)

	s.mu.Lock()

	lis := s.lis
	s.lis = nil

	conns := s.conns
	s.conns = nil

	s.mu.Unlock()

	if lis != nil {
		lis.Close()
	}

	m := mode
	if m == StopGracefullyAndWait {
		// don't wait each conn stop.
		m = StopGracefullyButNotWait
	}
	for c := range conns {
		c.Stop(m)
	}

	if mode == StopGracefullyAndWait {
		s.wg.Wait()
	}

	xlog.Info("XTCP server stop.")
}

func (s *Server) handleRawConn(conn net.Conn) {
	s.mu.Lock()
	if s.conns == nil {
		s.mu.Unlock()
		conn.Close()
		return
	}
	s.mu.Unlock()

	tcpConn, err := NewConn(s.Opts.Handler, s.Opts.Protocol, s.Opts.SendBufLen)
	if err != nil {
		return
	}
	tcpConn.RawConn = conn

	if !s.addConn(tcpConn) {
		tcpConn.Stop(StopImmediately)
		return
	}

	defer func() {
		s.removeConn(tcpConn)
		s.wg.Done()
	}()

	s.wg.Add(1)
	tcpConn.serve()
}

func (s *Server) addConn(conn *Conn) bool {
	s.mu.Lock()
	if s.conns == nil {
		s.mu.Unlock()
		return false
	}
	s.conns[conn] = true
	s.mu.Unlock()
	return true
}

func (s *Server) removeConn(conn *Conn) {
	s.mu.Lock()
	if s.conns != nil {
		delete(s.conns, conn)
	}
	s.mu.Unlock()
}

// NewServer create a tcp server but not start to accept.
func NewServer(opts *ServerOpts) *Server {
	s := &Server{
		Opts:  opts,
		stop:  make(chan struct{}),
		conns: make(map[*Conn]bool),
	}

	if s.Opts.SendBufLen == 0 {
		s.Opts.SendBufLen = DefaultSendBufLength
	}

	return s
}
