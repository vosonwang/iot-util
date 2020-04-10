package iot_util

import (
	"errors"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultMaxBytes = 500
	defaultTimeout  = 3 * time.Minute
)

type (
	Server struct {
		// Addr optionally specifies the TCP address for the server to listen on,
		// in the form "host:port". If empty, ":http" (port 80) is used.
		// The service names are defined in RFC 6335 and assigned by IANA.
		// See net.Dial for details of the address format.
		Addr string

		// 一次性读取字节流的最大长度，默认500个字节
		MaxBytes int

		// 读写超时设置，默认3分钟
		Timeout time.Duration

		// 处理从连接读取出的数据
		Handler func(c *Conn, out []byte)

		AfterConnClose func(id string)
		activeConn     sync.Map
		OnStart        func()
		// 是否打印报文
		debug bool
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

		// 用于和外界交换数据
		bridgeChan chan []byte

		// 资源读写锁
		sync.Mutex

		// 用于用户存储一些键值
		sync.Map
	}
)

func NewServer() *Server {
	return &Server{
		MaxBytes: defaultMaxBytes,
		Timeout:  defaultTimeout,
	}
}

func (srv *Server) Debug(debug bool) {
	srv.debug = debug
}

func (srv *Server) StartServer(address string) error {
	l, err := net.Listen("tcp", address)
	if err != nil {
		log.Printf("iot_util: Failed to Listen: %v", err)
		return err
	}
	defer l.Close()
	srv.OnStart()
	var tempDelay time.Duration // how long to sleep on accept failure
	for {
		rwc, err := l.Accept()
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
				log.Printf("iot_util: listen accept error: %v; retrying in %v", err, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return err
		}
		tempDelay = 0
		c := srv.newConn(rwc)
		srv.activeConn.Store(c, true)
		go c.serve()
	}
}

// Create new connection from rwc.
func (srv *Server) newConn(rwc net.Conn) *Conn {
	return &Conn{
		server:        srv,
		rwc:           rwc,
		CloseNotifier: make(chan bool),
		bridgeChan:    make(chan []byte),
	}
}

func (srv *Server) Shutdown() {
	srv.activeConn.Range(func(key, value interface{}) bool {
		key.(*Conn).Close()
		return true
	})
}

func (srv *Server) FindConn(id string) (*Conn, error) {
	c1 := new(Conn)
	srv.activeConn.Range(func(key, value interface{}) bool {
		c := key.(*Conn)
		if c.id == id {
			c1 = c
			return false
		}
		return true
	})
	if c1.id == "" {
		return nil, errors.New("设备离线")
	}
	return c1, nil
}

func (c *Conn) serve() {
	for {
		select {
		case <-c.CloseNotifier:
			return
		default:
			buf, err := c.read()
			if err != nil {
				log.Printf(`server: read from connection error: %v`, err)
				c.Close()
				return
			}
			c.server.Handler(c, buf)
		}
	}
}

func (c *Conn) ID() string {
	return c.id
}

func (c *Conn) SetID(id string) {
	c.server.activeConn.Range(func(key, value interface{}) bool {
		prev := key.(*Conn)
		if prev.id == id {
			prev.Close()
		}
		return false
	})
	c.id = id
}

func (c *Conn) Send(data []byte) error {
	select {
	case <-c.CloseNotifier:
		return errors.New("设备离线")
	case c.bridgeChan <- data:
		return nil
	case <-time.NewTicker(5 * time.Second).C:
		return errors.New("响应超时")
	}
}

func (c *Conn) Receive() ([]byte, error) {
	c.Lock()
	defer c.Unlock()
	select {
	case <-c.CloseNotifier:
		return nil, errors.New("设备离线")
	case buf := <-c.bridgeChan:
		return buf, nil
	case <-time.NewTicker(5 * time.Second).C:
		return nil, errors.New("响应超时")
	}
}

func (c *Conn) read() ([]byte, error) {
	buf := make([]byte, c.server.MaxBytes)
	defer func() {
		if c.server.debug {
			log.Printf(`read: % x`, buf)
		}
	}()
	c.rwc.SetReadDeadline(time.Now().Add(c.server.Timeout))
	readLen, err := c.rwc.Read(buf)
	if err != nil {
		return nil, err
	}
	buf = buf[:readLen]
	return buf, nil
}

func (c *Conn) Write(buf []byte) (n int, err error) {
	c.Lock()
	defer c.Unlock()
	// 防止粘包
	defer time.Sleep(1 * time.Second)
	defer func() {
		if c.server.debug {
			log.Printf(`write: % x`, buf)
		}
	}()
	c.rwc.SetWriteDeadline(time.Now().Add(c.server.Timeout))
	return c.rwc.Write(buf)
}

func (c *Conn) Close() {
	if !c.ShuttingDown() {
		atomic.StoreInt32(&c.inShutdown, 1)
		c.server.activeConn.Delete(c)
		close(c.CloseNotifier)
		c.rwc.Close()
		c.server.AfterConnClose(c.id)
	}
}

func (c *Conn) ShuttingDown() bool {
	// TODO: replace inShutdown with the existing atomicBool type;
	// see https://github.com/golang/go/issues/20239#issuecomment-381434582
	return atomic.LoadInt32(&c.inShutdown) != 0
}

// 获取客户端地址
func (c *Conn) RemoteAddr() string {
	return c.rwc.RemoteAddr().String()
}
