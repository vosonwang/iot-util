package iot_util

import "testing"

func TestBytesDecodeTime(t *testing.T) {
	got := BytesDecodeTime([]byte{20, 3, 12, 17, 19, 00})
	expect := "2020-03-12 17:19:00"
	if expect != got {
		t.Errorf("expected %x, got %x", expect, got)
	}
}
