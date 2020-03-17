package iot_util

import (
	"log"
	"testing"
)

func TestServer_Serve(t *testing.T) {
	s := NewServer()
	s.Handler = func(c *Conn) {

	}
	err := s.StartServer(":6500")
	if err != nil {
		// 建立连接
		log.Print(err.Error())
	}

}
