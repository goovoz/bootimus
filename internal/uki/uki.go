package uki

import (
	"bytes"
	"debug/pe"
	"errors"
	"strings"
)

var ErrNotUKI = errors.New("not a unified kernel image")

type Image struct {
	Cmdline string
}

func Inspect(path string) (*Image, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if f.Section(".linux") == nil {
		return nil, ErrNotUKI
	}

	img := &Image{}
	if s := f.Section(".cmdline"); s != nil {
		data, err := s.Data()
		if err != nil {
			return nil, err
		}
		if s.VirtualSize > 0 && int(s.VirtualSize) < len(data) {
			data = data[:s.VirtualSize]
		}
		data = bytes.TrimRight(data, "\x00")
		img.Cmdline = strings.Join(strings.Fields(string(data)), " ")
	}
	return img, nil
}

func IsUKI(path string) bool {
	_, err := Inspect(path)
	return err == nil
}
