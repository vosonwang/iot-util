package iot_util

import (
	"context"
	"errors"
	"log"
	"net"
	"sync/atomic"
	"time"
)

const (
	defaultMaxBytes = 500
	defaultTimeout  = 3 * time.Minute
)

type (
	Server struct {
		Addr           string
		MaxBytes       int
		Timeout        time.Duration
		HandleConn     func(c *Conn, out []byte) (in []byte, err error)
		AfterConnClose func(id string)
		ActiveConn     map[*Conn]bool
		OnStart        func()
	}

	// A conn represents the server side of an tcp connection.
	Conn struct {
		// 用于标示连接的唯一编号
		id string
		// server is the server on which the connection arrived.
		// Immutable; never nil.
		server *Server

		// rwc is the underlying network connection.
		// This is never wrapped by other types and is the value given out
		// to CloseNotifier callers. It is usually of type *net.TCPConn or
		// *tls.Conn.
		rwc net.Conn

		CloseNotifier chan bool

		inShutdown int32 // accessed atomically (non-zero means we're in Shutdown)

		requestChan chan []byte

		responseChan chan []byte

		metric []string

		// 用于发送和接收通过链接读写客户端的数据
		bridgeChan chan []byte

		Ctx context.Context
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
		log.Printf("iot_util: Failed to Listen: %v\n", err)
		return err
	}
	defer l.Close()
	srv.OnStart()
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
				log.Printf("iot_util: listen accept error: %v; retrying in %v\n", err, tempDelay)
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
	return &Conn{
		server:        srv,
		rwc:           rwc,
		requestChan:   make(chan []byte),
		responseChan:  make(chan []byte),
		CloseNotifier: make(chan bool),
		bridgeChan:    make(chan []byte),
		Ctx:           context.Background(),
	}
}

func (srv *Server) Shutdown() {
	for c := range srv.ActiveConn {
		c.Close()
	}
}

func (c *Conn) ID() string {
	return c.id
}

func (c *Conn) Setid(id string) {
	for prev := range c.server.ActiveConn {
		if prev.id == id {
			prev.Close()
			break
		}
	}
	c.id = id
}

func (c *Conn) Send(data []byte) error {
	select {
	case c.bridgeChan <- data:
		return nil
	case <-time.NewTicker(5 * time.Second).C:
		return errors.New("响应超时")
	}
}

func (c *Conn) Receive() ([]byte, error) {
	select {
	case buf := <-c.bridgeChan:
		return buf, nil
	case <-time.NewTicker(5 * time.Second).C:
		return nil, errors.New("响应超时")
	}
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
					log.Printf(`iot_util: read from connection error %v\n`, err)
					c.Close()
					return
				}
				c.responseChan <- buf
			}
		}
	}()

	// write
	go func() {
		for {
			select {
			case <-c.CloseNotifier:
				return
			case buf := <-c.requestChan:
				if err := c.write(buf); err != nil {
					log.Printf(`iot_util: write to connection error %v\n`, err)
					c.Close()
					return
				}
				// 防止粘包
				time.Sleep(1 * time.Second)
			}
		}
	}()

	// handle response
	go func() {
		for {
			select {
			case <-c.CloseNotifier:
				return
			case buf := <-c.responseChan:
				if resp, err := c.server.HandleConn(c, buf); err != nil {
					log.Printf(`iot_util:handle response error %v\n`, err)
				} else {
					if resp != nil {
						c.requestChan <- resp
					}
				}
			}
		}
	}()

	<-c.CloseNotifier
}

func (c *Conn) GetRequest(buf []byte) error {
	select {
	case c.requestChan <- buf:
		return nil
	case <-time.NewTicker(5 * time.Second).C:
		return errors.New("请求超时")
	}
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
	if !c.shuttingDown() {
		atomic.StoreInt32(&c.inShutdown, 1)
		delete(c.server.ActiveConn, c)
		close(c.CloseNotifier)
		c.rwc.Close()
		c.server.AfterConnClose(c.id)
	}
}

func (c *Conn) shuttingDown() bool {
	// TODO: replace inShutdown with the existing atomicBool type;
	// see https://github.com/golang/go/issues/20239#issuecomment-381434582
	return atomic.LoadInt32(&c.inShutdown) != 0
}
