package iot_util

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"testing"
)

func TestServer_Serve(t *testing.T) {
	s := NewServer()
	s.Handler = func(c *Conn, out []byte) {
		// handle response
	}
	s.AfterConnClose = func(sn string) {
		// do something
	}
	s.OnStart = func() {
		// do something
	}

	activeConn := make(map[*Conn]bool)
	s.ActiveConn = activeConn

	go func() {
		err := s.StartServer("6500")
		if err != nil {
			log.Print(err.Error())
		}
	}()

	// gracefully shutdown
	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 10 seconds.
	quit := make(chan os.Signal)
	signal.Notify(quit, syscall.SIGTERM, os.Interrupt)
	<-quit
	s.Shutdown()
}

type word struct {
	Name  string
	Start uint16
}

func (w word) GetName() string {
	return w.Name
}

func (w word) GetStart() uint16 {
	return w.Start
}

func (w word) GetNum() uint16 {
	return 1
}

var (
	IRSwitch = &word{
		Name:  "IRSwitch",
		Start: 67,
	}

	OCSwitch = &word{
		Name:  "OCSwitch",
		Start: 76,
	}

	CableTemperatureSwitch = &word{
		Name:  "CableTemperatureSwitch",
		Start: 79,
	}

	OVSwitch = &word{
		Name:  "OVSwitch",
		Start: 70,
	}

	UASwitch = &word{
		Name:  "UASwitch",
		Start: 73,
	}
)

func TestGetLastRegister(t *testing.T) {
	rs := Registers{IRSwitch, OCSwitch, CableTemperatureSwitch, OVSwitch, UASwitch}
	got := rs.getLastRegister().GetStart()
	expect := uint16(79)
	if got != expect {
		t.Errorf(`expect:%v,got:%v`, expect, got)
	}
}
