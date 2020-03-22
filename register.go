package iot_util

import (
	"errors"
	"strings"
)

type (
	Register interface {
		GetName() string
		GetStart() uint16 // 获取寄存器起始地址
		GetNum() uint16   // 获取寄存器数量
	}

	Registers []Register

	decoder interface {
		Decode(data []byte, m map[string]interface{})
	}

	encoder interface {
		Encode(value string) ([]byte, error)
	}
)

func (rs Registers) Encode(value string) ([]byte, error) {
	vals := strings.Split(value, ",")
	if len(rs) != len(vals) {
		return nil, errors.New("参数个数不匹配")
	}
	buf := make([]byte, rs.GetNum()*2)
	for index, r := range rs {
		if w, ok := r.(encoder); !ok {
			return nil, errors.New("请求中存在不支持写入的指标")
		} else {
			b, err := w.Encode(vals[index])
			if err != nil {
				return nil, err
			}
			start := (r.GetStart() - rs.GetStart()) * 2
			end := start + r.GetNum()*2
			// 这样写，就不用担心rs数组中各个寄存器的排列顺序了
			copy(buf[start:end], b)
		}
	}
	return buf, nil
}

func (rs Registers) decode(data []byte) (map[string]interface{}, error) {
	m := make(map[string]interface{})
	for _, r := range rs {
		if ro, ok := r.(decoder); !ok {
			return nil, errors.New("请求中存在不支持读取的指标")
		} else {
			start := (r.GetStart() - rs.GetStart()) * 2
			end := start + r.GetNum()*2
			ro.Decode(data[start:end], m)
		}
	}
	return m, nil
}

func (rs Registers) GetStart() uint16 {
	min := rs[0].GetStart()
	for _, r := range rs {
		s := r.GetStart()
		if min > s {
			min = s
		}
	}
	return min
}

func (rs Registers) getLastRegister() (last Register) {
	max := rs[0].GetStart()
	last = rs[0]
	for _, r := range rs {
		s := r.GetStart()
		if max < s {
			last = r
		}
	}
	return
}

func (rs Registers) GetNum() uint16 {
	last := rs.getLastRegister()
	return last.GetStart() + last.GetNum() - rs.GetStart()
}
