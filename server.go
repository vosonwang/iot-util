package iot_util

import (
	"log"
	"net"
	"time"
)

const (
	defaultMaxBytes = 500
	defaultTimeout  = 3 * time.Minute
)

type (
	Server struct {
		Addr       string
		MaxBytes   int
		Timeout    time.Duration
		Handler    func(c *Conn)
		ActiveConn map[*Conn]bool
	}

	// A conn represents the server side of an tcp connection.
	Conn struct {
		SN string
		// server is the server on which the connection arrived.
		// Immutable; never nil.
		server *Server

		// rwc is the underlying network connection.
		// This is never wrapped by other types and is the value given out
		// to CloseNotifier callers. It is usually of type *net.TCPConn or
		// *tls.Conn.
		rwc net.Conn

		CloseNotifier chan bool

		RequestChan chan []byte

		ResponseChan chan []byte
	}
)

func NewServer() *Server {
	return &Server{
		MaxBytes: defaultMaxBytes,
		Timeout:  defaultTimeout,
	}
}

func (srv *Server) StartServer(address string) error {
	l, err := net.Listen("tcp", address)
	if err != nil {
		log.Printf("Failed to Listen: %v\n", err)
		return err
	}
	defer l.Close()
	var tempDelay time.Duration // how long to sleep on accept failure
	for {
		rw, err := l.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				log.Printf("http: Accept error: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		c := srv.newConn(rw)
		srv.ActiveConn[c] = true
		go c.serve()
	}
}

// Create new connection from rwc.
func (srv *Server) newConn(rwc net.Conn) *Conn {
	c := &Conn{
		server:        srv,
		rwc:           rwc,
		RequestChan:   make(chan []byte),
		ResponseChan:  make(chan []byte),
		CloseNotifier: make(chan bool),
	}
	return c
}

func (c *Conn) serve() {
	// read
	go func() {
		for {
			select {
			case <-c.CloseNotifier:
				return
			default:
				buf, err := c.read()
				if err != nil {
					c.Close()
					return
				}
				c.ResponseChan <- buf
			}
		}
	}()

	// write
	go func() {
		for {
			select {
			case <-c.CloseNotifier:
				return
			case buf := <-c.RequestChan:
				if err := c.write(buf); err != nil {
					c.Close()
					return
				}
				// 防止粘包
				time.Sleep(1 * time.Second)
			}
		}
	}()

	go c.server.Handler(c)

	<-c.CloseNotifier
}

func (c *Conn) read() ([]byte, error) {
	buf := make([]byte, c.server.MaxBytes)
	c.rwc.SetReadDeadline(time.Now().Add(c.server.Timeout))
	readLen, err := c.rwc.Read(buf)
	if err != nil {
		return nil, err
	}
	buf = buf[:readLen]
	return buf, nil
}

func (c *Conn) write(buf []byte) error {
	c.rwc.SetWriteDeadline(time.Now().Add(c.server.Timeout))
	_, err := c.rwc.Write(buf)
	return err
}

func (c *Conn) Close() {
	close(c.CloseNotifier)
	delete(c.server.ActiveConn, c)
}

func (c *Conn) SetSN(sn string) {
	for c2 := range c.server.ActiveConn {
		if c2.SN == sn {
			delete(c.server.ActiveConn, c2)
		}
	}
	c.SN = sn
}
